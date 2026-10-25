package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// dsnPragmas are applied by the driver to every new connection in the pool.
const dsnPragmas = "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"

func dsn(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + dsnPragmas
}

// OpenDB opens the SQLite database at path and runs all pending schema
// migrations. It returns the open *sql.DB to the caller.
func OpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	// Single writer, and ":memory:" gives each connection its own database.
	// Foreign keys are enforced via the DSN, not because of this limit.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type migration struct {
	name      string
	disableFK bool // table rebuilds: run with foreign_keys=OFF around the tx
	up        func(tx *sql.Tx) error
}

// migrations is APPEND ONLY: index i is schema version i+1.
var migrations = []migration{
	{name: "baseline", up: migrateBaseline},
	{name: "sync_low_threshold", up: migrateSyncLowThreshold},
	{name: "normalize_meal_cooked_at", up: normalizeMealCookedAt},
	{name: "inventory_fk_on_delete_set_null", disableFK: true, up: migrateInventoryFKSetNull},
}

// LatestVersion is the schema version produced by all known migrations.
func LatestVersion() int { return len(migrations) }

func migrate(db *sql.DB) (err error) {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	var cur int
	if err := conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&cur); err != nil {
		return err
	}
	if cur > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this binary supports (%d)", cur, len(migrations))
	}
	for i := cur; i < len(migrations); i++ {
		if err := runMigration(ctx, conn, i+1, migrations[i]); err != nil {
			return err
		}
		log.Printf("db: applied migration %d (%s)", i+1, migrations[i].name)
	}
	return nil
}

func runMigration(ctx context.Context, conn *sql.Conn, v int, m migration) (err error) {
	if m.disableFK {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
			return err
		}
		defer func() {
			if _, ferr := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); ferr != nil && err == nil {
				err = ferr
			}
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := m.up(tx); err != nil {
		tx.Rollback()
		return fmt.Errorf("migration %d (%s): %w", v, m.name, err)
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
		tx.Rollback()
		return fmt.Errorf("migration %d (%s): %w", v, m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %d (%s): %w", v, m.name, err)
	}
	return nil
}

func columnExists(tx *sql.Tx, table, col string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, col).Scan(&n)
	return n > 0, err
}

// addColumnIfMissing: table and ddl are code constants, never user input.
func addColumnIfMissing(tx *sql.Tx, table, col, ddl string) error {
	ok, err := columnExists(tx, table, col)
	if err != nil || ok {
		return err
	}
	_, err = tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + ddl)
	return err
}

// migrateBaseline is idempotent so it works for empty DBs and for DBs built
// by the pre-versioning code (user_version 0).
func migrateBaseline(tx *sql.Tx) error {
	if _, err := tx.Exec(baselineSQL); err != nil {
		return err
	}
	for _, c := range [][3]string{
		{"inventory", "preferred_unit", "preferred_unit TEXT NOT NULL DEFAULT ''"},
		{"inventory", "unit_cost_cents", "unit_cost_cents INTEGER NOT NULL DEFAULT 0"},
		{"inventory_history", "recipe_id", "recipe_id INTEGER REFERENCES recipes(id)"},
		{"inventory", "quantity_per_scan", "quantity_per_scan REAL NOT NULL DEFAULT 1"},
	} {
		if err := addColumnIfMissing(tx, c[0], c[1], c[2]); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS inventory_skus (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id      INTEGER NOT NULL REFERENCES inventory(id) ON DELETE CASCADE,
		barcode           TEXT    NOT NULL UNIQUE,
		quantity_per_scan REAL    NOT NULL DEFAULT 1
	)`); err != nil {
		return err
	}
	_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS inventory_skus_barcode_idx ON inventory_skus(barcode)`)
	return err
}

// Par level is per product (name); keep lots consistent until products/lots split.
func migrateSyncLowThreshold(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE inventory SET low_threshold = (SELECT MAX(i2.low_threshold) FROM inventory i2 WHERE i2.name = inventory.name)
		WHERE low_threshold <> (SELECT MAX(i2.low_threshold) FROM inventory i2 WHERE i2.name = inventory.name)`)
	return err
}

// migrateInventoryFKSetNull rebuilds the tables referencing inventory(id) so
// the FK is ON DELETE SET NULL. Runs with foreign_keys=OFF (see runMigration).
func migrateInventoryFKSetNull(tx *sql.Tx) error {
	const nullDangling = `CASE WHEN inventory_id IN (SELECT id FROM inventory) THEN inventory_id END`
	steps := []string{
		`CREATE TABLE shopping_list_new (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			inventory_id     INTEGER REFERENCES inventory(id) ON DELETE SET NULL,
			name             TEXT    NOT NULL,
			quantity_needed  REAL    NOT NULL DEFAULT 1,
			unit             TEXT    NOT NULL DEFAULT '',
			checked          INTEGER NOT NULL DEFAULT 0,
			source           TEXT    NOT NULL DEFAULT 'manual'
		)`,
		`INSERT INTO shopping_list_new (id, inventory_id, name, quantity_needed, unit, checked, source)
			SELECT id, ` + nullDangling + `, name, quantity_needed, unit, checked, source FROM shopping_list`,
		`DROP TABLE shopping_list`,
		`ALTER TABLE shopping_list_new RENAME TO shopping_list`,

		`CREATE TABLE recipe_ingredients_new (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			recipe_id    INTEGER NOT NULL REFERENCES recipes(id),
			inventory_id INTEGER REFERENCES inventory(id) ON DELETE SET NULL,
			name         TEXT    NOT NULL,
			quantity     REAL    NOT NULL,
			unit         TEXT    NOT NULL DEFAULT ''
		)`,
		`INSERT INTO recipe_ingredients_new (id, recipe_id, inventory_id, name, quantity, unit)
			SELECT id, recipe_id, ` + nullDangling + `, name, quantity, unit FROM recipe_ingredients`,
		`DROP TABLE recipe_ingredients`,
		`ALTER TABLE recipe_ingredients_new RENAME TO recipe_ingredients`,

		`CREATE TABLE meal_history_ingredients_new (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			meal_history_id INTEGER NOT NULL REFERENCES meal_history(id),
			inventory_id    INTEGER REFERENCES inventory(id) ON DELETE SET NULL,
			ingredient_name TEXT    NOT NULL,
			quantity_used   REAL    NOT NULL,
			unit            TEXT    NOT NULL DEFAULT '',
			cost_cents      INTEGER
		)`,
		`INSERT INTO meal_history_ingredients_new (id, meal_history_id, inventory_id, ingredient_name, quantity_used, unit, cost_cents)
			SELECT id, meal_history_id, ` + nullDangling + `, ingredient_name, quantity_used, unit, cost_cents FROM meal_history_ingredients`,
		`DROP TABLE meal_history_ingredients`,
		`ALTER TABLE meal_history_ingredients_new RENAME TO meal_history_ingredients`,
	}
	for _, s := range steps {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	// Pre-existing dangling refs (e.g. recipe_id) are logged, not fatal, so
	// startup never fails on old bad data.
	for _, t := range []string{"shopping_list", "recipe_ingredients", "meal_history_ingredients"} {
		rows, err := tx.Query(`PRAGMA foreign_key_check(` + t + `)`)
		if err != nil {
			return err
		}
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		if n > 0 {
			log.Printf("db: %d pre-existing foreign key violations in %s (left as is)", n, t)
		}
	}
	return nil
}

const baselineSQL = `
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
`

// normalizeMealCookedAt rewrites legacy meal_history.cooked_at values into the
// canonical form: RFC3339 UTC with Z suffix (2006-01-02T15:04:05Z).
func normalizeMealCookedAt(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id, CAST(cooked_at AS TEXT) FROM meal_history
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
	for _, u := range ups {
		if _, err := tx.Exec(`UPDATE meal_history SET cooked_at=? WHERE id=?`, u.v, u.id); err != nil {
			return err
		}
	}
	log.Printf("normalized cooked_at on %d meal_history rows", len(ups))
	return nil
}
