package database

import (
	"database/sql"
	"strings"
	_ "modernc.org/sqlite"
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
		low_threshold   REAL    NOT NULL DEFAULT 1,
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
		recipe_id        INTEGER NOT NULL REFERENCES recipes(id),
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

	return nil
}
