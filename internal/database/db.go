package database

import (
	"context"
	"database/sql"
	"log"
	_ "modernc.org/sqlite"
	"strings"
	"time"
)

// OpenDB opens the SQLite database at path, sets connection limits, and
// runs all schema migrations. It returns the open *sql.DB to the caller.
func OpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: single writer
	if err := createSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func createSchema(db *sql.DB) error {
	_, err := db.Exec(`
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS inventory (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		name            TEXT    NOT NULL,
		quantity        REAL    NOT NULL DEFAULT 0,
		unit            TEXT    NOT NULL DEFAULT '',
		location        TEXT    NOT NULL DEFAULT '',
		expiration_date TEXT    NOT NULL DEFAULT '',
		low_threshold   REAL    NOT NULL DEFAULT 0,
		barcode         TEXT    NOT NULL DEFAULT '',
		preferred_unit  TEXT    NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS shopping_list (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id     INTEGER REFERENCES inventory(id),
		name             TEXT    NOT NULL,
		quantity_needed  REAL    NOT NULL DEFAULT 1,
		unit             TEXT    NOT NULL DEFAULT '',
		checked          INTEGER NOT NULL DEFAULT 0,
		source           TEXT    NOT NULL DEFAULT 'manual'
	);

	CREATE TABLE IF NOT EXISTS recipes (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		name         TEXT    NOT NULL,
		description  TEXT    NOT NULL DEFAULT '',
		instructions TEXT    NOT NULL DEFAULT '',
		tags         TEXT    NOT NULL DEFAULT '',
		servings     INTEGER NOT NULL DEFAULT 1
	);

	CREATE TABLE IF NOT EXISTS recipe_ingredients (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		recipe_id    INTEGER NOT NULL REFERENCES recipes(id),
		inventory_id INTEGER REFERENCES inventory(id),
		name         TEXT    NOT NULL,
		quantity     REAL    NOT NULL,
		unit         TEXT    NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS meal_calendar (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		date      TEXT    NOT NULL,
		meal_slot TEXT    NOT NULL DEFAULT 'dinner',
		recipe_id INTEGER NOT NULL REFERENCES recipes(id),
		servings  INTEGER NOT NULL DEFAULT 1
	);

	CREATE TABLE IF NOT EXISTS sessions (
		token  TEXT PRIMARY KEY,
		data   BLOB NOT NULL,
		expiry DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions(expiry);

	CREATE TABLE IF NOT EXISTS inventory_history (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id     INTEGER NOT NULL,
		item_name        TEXT    NOT NULL,
		changed_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
		changed_by       TEXT,
		change_type      TEXT    NOT NULL,
		quantity_before  REAL,
		quantity_after   REAL,
		unit             TEXT    NOT NULL DEFAULT '',
		source           TEXT
	);

	CREATE INDEX IF NOT EXISTS inventory_history_item_idx
		ON inventory_history(inventory_id, changed_at);

	CREATE INDEX IF NOT EXISTS inventory_history_changed_at_idx
		ON inventory_history(changed_at);

	CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL DEFAULT ''
	);

	INSERT OR IGNORE INTO settings (key, value) VALUES ('default_servings', '2');

	CREATE TABLE IF NOT EXISTS meal_history (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		recipe_id        INTEGER REFERENCES recipes(id) ON DELETE SET NULL,
		recipe_name      TEXT    NOT NULL DEFAULT '',
		cooked_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		servings_made    INTEGER NOT NULL DEFAULT 1,
		total_cost_cents INTEGER,
		notes            TEXT    NOT NULL DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS meal_history_recipe_idx
		ON meal_history(recipe_id, cooked_at);

	CREATE INDEX IF NOT EXISTS meal_history_cooked_at_idx
		ON meal_history(cooked_at);

	CREATE TABLE IF NOT EXISTS meal_history_ingredients (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		meal_history_id INTEGER NOT NULL REFERENCES meal_history(id),
		inventory_id    INTEGER REFERENCES inventory(id),
		ingredient_name TEXT    NOT NULL,
		quantity_used   REAL    NOT NULL,
		unit            TEXT    NOT NULL DEFAULT '',
		cost_cents      INTEGER
	);
	`)
	if err != nil {
		return err
	}
	// Migration: add preferred_unit to inventory for databases created before this column
	rows, err := db.Query(`PRAGMA table_info(inventory)`)
	if err != nil {
		return err
	}
	var cols []string
	for rows.Next() {
		var cid int
		var name, colType, notNull, pk string
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			rows.Close()
			return err
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	hasPrefUnit := false
	for _, c := range cols {
		if c == "preferred_unit" {
			hasPrefUnit = true
			break
		}
	}
	if !hasPrefUnit {
		if _, err := db.Exec(`ALTER TABLE inventory ADD COLUMN preferred_unit TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}

	// Migration: unit_cost_cents on inventory
	if _, err := db.Exec(`ALTER TABLE inventory ADD COLUMN unit_cost_cents INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}

	// Migration: recipe_id on inventory_history (for meal cooking source)
	if _, err := db.Exec(`ALTER TABLE inventory_history ADD COLUMN recipe_id INTEGER REFERENCES recipes(id)`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}

	// Migration: quantity_per_scan on inventory
	if _, err := db.Exec(`ALTER TABLE inventory ADD COLUMN quantity_per_scan REAL NOT NULL DEFAULT 1`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}

	// Migration: inventory_skus table for barcode aliases
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS inventory_skus (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id      INTEGER NOT NULL REFERENCES inventory(id) ON DELETE CASCADE,
		barcode           TEXT    NOT NULL UNIQUE,
		quantity_per_scan REAL    NOT NULL DEFAULT 1
	)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS inventory_skus_barcode_idx ON inventory_skus(barcode)`); err != nil {
		return err
	}

	// Migration: archived flag on recipes
	if _, err := db.Exec(`ALTER TABLE recipes ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}

	if err := migrateMealHistoryNullableRecipe(db); err != nil {
		return err
	}

	// Migration: link meal_history to the calendar entry it was cooked from.
	// Must run after migrateMealHistoryNullableRecipe (which rebuilds the table).
	if _, err := db.Exec(`ALTER TABLE meal_history ADD COLUMN calendar_entry_id INTEGER REFERENCES meal_calendar(id) ON DELETE SET NULL`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS meal_history_calendar_entry_idx ON meal_history(calendar_entry_id) WHERE calendar_entry_id IS NOT NULL`); err != nil {
		return err
	}
	if err := normalizeMealSlots(db); err != nil {
		return err
	}

	// Par level is per product (name); keep lots consistent until products/lots split.
	if _, err := db.Exec(`UPDATE inventory SET low_threshold = (SELECT MAX(i2.low_threshold) FROM inventory i2 WHERE i2.name = inventory.name)
		WHERE low_threshold <> (SELECT MAX(i2.low_threshold) FROM inventory i2 WHERE i2.name = inventory.name)`); err != nil {
		return err
	}

	if err := normalizeMealCookedAt(db); err != nil {
		return err
	}

	return nil
}

// normalizeMealSlots rewrites legacy free-text meal slots into the fixed set
// (breakfast, lunch, dinner, snack) where they match; others are left as-is.
func normalizeMealSlots(db *sql.DB) error {
	for _, q := range []string{
		`UPDATE meal_calendar SET meal_slot = LOWER(TRIM(meal_slot))
			WHERE LOWER(TRIM(meal_slot)) IN ('breakfast','lunch','dinner','snack') AND meal_slot <> LOWER(TRIM(meal_slot))`,
		`UPDATE meal_calendar SET meal_slot='snack' WHERE LOWER(TRIM(meal_slot))='snacks'`,
		`UPDATE meal_calendar SET meal_slot='dinner' WHERE LOWER(TRIM(meal_slot))='supper'`,
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// normalizeMealCookedAt rewrites legacy meal_history.cooked_at values into the
// canonical form: RFC3339 UTC with Z suffix (2006-01-02T15:04:05Z).
func normalizeMealCookedAt(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, CAST(cooked_at AS TEXT) FROM meal_history
		WHERE CAST(cooked_at AS TEXT) NOT GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]Z'`)
	if err != nil {
		return err
	}
	type upd struct {
		id int64
		v  string
	}
	var ups []upd
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05",
		"2006-01-02 15:04:05Z07:00", "2006-01-02"}
	for rows.Next() {
		var id int64
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			rows.Close()
			return err
		}
		for _, l := range layouts {
			if t, err := time.Parse(l, v); err == nil {
				ups = append(ups, upd{id, t.UTC().Format(time.RFC3339)})
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(ups) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, u := range ups {
		if _, err := tx.Exec(`UPDATE meal_history SET cooked_at=? WHERE id=?`, u.v, u.id); err != nil {
			tx.Rollback()
			return err
		}
	}
	log.Printf("normalized cooked_at on %d meal_history rows", len(ups))
	return tx.Commit()
}

// migrateMealHistoryNullableRecipe rebuilds meal_history on legacy databases
// where recipe_id was NOT NULL, so deleting a recipe can keep its history.
func migrateMealHistoryNullableRecipe(db *sql.DB) error {
	needs := false
	rows, err := db.Query(`PRAGMA table_info(meal_history)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "recipe_id" && notnull == 1 {
			needs = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !needs {
		return nil
	}

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmts := []string{
		`CREATE TABLE meal_history_new (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			recipe_id        INTEGER REFERENCES recipes(id) ON DELETE SET NULL,
			recipe_name      TEXT    NOT NULL DEFAULT '',
			cooked_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			servings_made    INTEGER NOT NULL DEFAULT 1,
			total_cost_cents INTEGER,
			notes            TEXT    NOT NULL DEFAULT ''
		)`,
		`INSERT INTO meal_history_new (id,recipe_id,recipe_name,cooked_at,servings_made,total_cost_cents,notes)
			SELECT id,recipe_id,recipe_name,cooked_at,servings_made,total_cost_cents,notes FROM meal_history`,
		`DROP TABLE meal_history`,
		`ALTER TABLE meal_history_new RENAME TO meal_history`,
		`CREATE INDEX IF NOT EXISTS meal_history_recipe_idx ON meal_history(recipe_id, cooked_at)`,
		`CREATE INDEX IF NOT EXISTS meal_history_cooked_at_idx ON meal_history(cooked_at)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
