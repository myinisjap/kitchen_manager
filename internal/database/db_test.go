package database

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// legacyBaseSQL is the schema block the pre-versioning createSchema produced
// (before the ALTER-based column additions).
const legacyBaseSQL = `
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

const legacyColsSQL = `
	ALTER TABLE inventory ADD COLUMN unit_cost_cents INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE inventory_history ADD COLUMN recipe_id INTEGER REFERENCES recipes(id);
	ALTER TABLE inventory ADD COLUMN quantity_per_scan REAL NOT NULL DEFAULT 1;
	CREATE TABLE inventory_skus (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id      INTEGER NOT NULL REFERENCES inventory(id) ON DELETE CASCADE,
		barcode           TEXT    NOT NULL UNIQUE,
		quantity_per_scan REAL    NOT NULL DEFAULT 1
	);
	CREATE INDEX inventory_skus_barcode_idx ON inventory_skus(barcode);
`

const legacySeedSQL = `
	INSERT INTO inventory (id, name, low_threshold) VALUES (1, 'flour', 1), (2, 'rice', 1), (3, 'rice', 5);
	INSERT INTO recipes (id, name) VALUES (1, 'r');
	INSERT INTO shopping_list (inventory_id, name) VALUES (1, 'flour'), (999, 'ghost');
	INSERT INTO recipe_ingredients (recipe_id, inventory_id, name, quantity) VALUES (1, 1, 'flour', 2);
	INSERT INTO meal_history (id, recipe_id, recipe_name, cooked_at) VALUES (1, 1, 'r', '2024-03-05 12:00:00');
	INSERT INTO meal_history_ingredients (meal_history_id, inventory_id, ingredient_name, quantity_used) VALUES (1, 1, 'flour', 2);
`

// makeLegacyDB builds a user_version-0 DB without DSN pragmas. If withCols,
// it has all the later-added columns and inventory_skus (prod state).
func makeLegacyDB(t *testing.T, path string, withCols bool) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := legacyBaseSQL
	if !withCols {
		base = strings.Replace(base, ",\n\t\tpreferred_unit  TEXT    NOT NULL DEFAULT ''", "", 1)
		if strings.Contains(base, "preferred_unit") {
			t.Fatal("fixture: preferred_unit not removed")
		}
	}
	stmts := []string{base}
	if withCols {
		stmts = append(stmts, legacyColsSQL)
	}
	stmts = append(stmts, legacySeedSQL)
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestOpenDBSchema(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, tbl := range []string{"inventory", "recipes", "recipe_ingredients", "shopping_list", "meal_calendar", "meal_history", "inventory_history", "settings", "sessions", "inventory_skus"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d err=%v)", tbl, n, err)
		}
	}
	var v string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='default_servings'`).Scan(&v); err != nil || v != "2" {
		t.Errorf("default_servings seed: %q %v", v, err)
	}
}

func TestPragmasEveryConnection(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(3)
	ctx := context.Background()
	var conns []*sql.Conn
	for i := 0; i < 3; i++ {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
	}
	for i, c := range conns {
		var fk, bt int
		var jm string
		if err := c.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
			t.Errorf("conn %d foreign_keys=%d %v", i, fk, err)
		}
		if err := c.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&bt); err != nil || bt != 5000 {
			t.Errorf("conn %d busy_timeout=%d %v", i, bt, err)
		}
		if err := c.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&jm); err != nil || jm != "wal" {
			t.Errorf("conn %d journal_mode=%q %v", i, jm, err)
		}
	}
}

func checkSetNull(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, tbl := range []string{"shopping_list", "recipe_ingredients", "meal_history_ingredients"} {
		if n := count(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_list(?) WHERE "table"='inventory' AND on_delete='SET NULL'`, tbl); n != 1 {
			t.Errorf("%s: inventory FK not ON DELETE SET NULL", tbl)
		}
	}
}

func checkColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, c := range [][2]string{{"inventory", "preferred_unit"}, {"inventory", "unit_cost_cents"}, {"inventory", "quantity_per_scan"}, {"inventory_history", "recipe_id"}} {
		if n := count(t, db, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, c[0], c[1]); n != 1 {
			t.Errorf("column %s.%s missing", c[0], c[1])
		}
	}
}

func TestFreshDBMigratesToLatest(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v := count(t, db, `PRAGMA user_version`); v != LatestVersion() {
		t.Errorf("user_version=%d want %d", v, LatestVersion())
	}
	checkColumns(t, db)
	checkSetNull(t, db)
}

func TestLegacyDBMigrates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		withCols bool
	}{{"pre-columns", false}, {"current-prod", true}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "k.db")
			makeLegacyDB(t, path, tc.withCols)
			db, err := OpenDB(path)
			if err != nil {
				t.Fatal(err)
			}
			if v := count(t, db, `PRAGMA user_version`); v != LatestVersion() {
				t.Errorf("user_version=%d", v)
			}
			if count(t, db, `PRAGMA foreign_keys`) != 1 {
				t.Error("foreign_keys off")
			}
			checkColumns(t, db)
			checkSetNull(t, db)
			if n := count(t, db, `SELECT COUNT(*) FROM shopping_list`); n != 2 {
				t.Errorf("shopping_list rows=%d", n)
			}
			if n := count(t, db, `SELECT COUNT(*) FROM shopping_list WHERE inventory_id IS NULL AND name='ghost'`); n != 1 {
				t.Error("dangling ref not nulled")
			}
			if n := count(t, db, `SELECT COUNT(*) FROM recipe_ingredients`); n != 1 {
				t.Errorf("recipe_ingredients rows=%d", n)
			}
			if n := count(t, db, `SELECT COUNT(*) FROM meal_history_ingredients`); n != 1 {
				t.Errorf("mhi rows=%d", n)
			}
			if n := count(t, db, `SELECT COUNT(*) FROM inventory WHERE name='rice' AND low_threshold=5`); n != 2 {
				t.Errorf("low_threshold not equalized")
			}
			var got string
			if err := db.QueryRow(`SELECT cooked_at FROM meal_history WHERE id=1`).Scan(&got); err != nil || got != "2024-03-05T12:00:00Z" {
				t.Errorf("cooked_at=%q %v", got, err)
			}
			if _, err := db.Exec(`DELETE FROM inventory WHERE id=1`); err != nil {
				t.Fatalf("delete inventory: %v", err)
			}
			for _, tbl := range []string{"shopping_list", "recipe_ingredients", "meal_history_ingredients"} {
				if n := count(t, db, `SELECT COUNT(*) FROM `+tbl+` WHERE inventory_id IS NOT NULL`); n != 0 {
					t.Errorf("%s still has refs", tbl)
				}
			}
			db.Close()

			db, err = OpenDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if v := count(t, db, `PRAGMA user_version`); v != LatestVersion() {
				t.Errorf("reopen user_version=%d", v)
			}
		})
	}
}

func TestNewerSchemaVersionRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA user_version=999`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if db, err := OpenDB(path); err == nil {
		db.Close()
		t.Fatal("expected error for newer schema")
	}
}

func TestNormalizeMealCookedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.db")
	makeLegacyDB(t, path, true)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM meal_history_ingredients; DELETE FROM meal_history`); err != nil {
		t.Fatal(err)
	}
	in := []string{"2024-03-05T07:00:00-05:00", "2024-03-05 12:00:00", "2024-03-05T12:00:00.123Z", "2024-03-05T12:00:00Z", "garbage"}
	for _, v := range in {
		if _, err := raw.Exec(`INSERT INTO meal_history (recipe_id, recipe_name, cooked_at, servings_made) VALUES (1,'r',?,1)`, v); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	for i := 0; i < 2; i++ {
		db, err := OpenDB(path)
		if err != nil {
			t.Fatal(err)
		}
		for j := range in {
			var got string
			if err := db.QueryRow(`SELECT cooked_at FROM meal_history ORDER BY id LIMIT 1 OFFSET ?`, j).Scan(&got); err != nil {
				t.Fatal(err)
			}
			want := "2024-03-05T12:00:00Z"
			if j == 4 {
				want = "garbage"
			}
			if got != want {
				t.Errorf("pass %d row %d: %q want %q", i, j, got, want)
			}
		}
		db.Close()
	}
}

func notNull(t *testing.T, db *sql.DB, table, col string) int {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var d sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &nn, &d, &pk); err != nil {
			t.Fatal(err)
		}
		if name == col {
			return nn
		}
	}
	return -1
}

func TestSchemaArchivedAndNullableHistory(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if notNull(t, db, "recipes", "archived") != 1 {
		t.Error("recipes.archived missing")
	}
	if notNull(t, db, "meal_history", "recipe_id") != 0 {
		t.Error("meal_history.recipe_id should be nullable")
	}
}

func TestMigrateMealHistoryNullableRecipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy (pre-migration) layout: meal_history.recipe_id is NOT NULL and
	// recipes has no archived column. Hand-written on purpose.
	_, err = old.Exec(`
	CREATE TABLE recipes (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '', instructions TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '', servings INTEGER NOT NULL DEFAULT 1);
	CREATE TABLE meal_history (id INTEGER PRIMARY KEY AUTOINCREMENT,
		recipe_id INTEGER NOT NULL REFERENCES recipes(id), recipe_name TEXT NOT NULL DEFAULT '',
		cooked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, servings_made INTEGER NOT NULL DEFAULT 1,
		total_cost_cents INTEGER, notes TEXT NOT NULL DEFAULT '');
	CREATE TABLE meal_history_ingredients (id INTEGER PRIMARY KEY AUTOINCREMENT,
		meal_history_id INTEGER NOT NULL REFERENCES meal_history(id), inventory_id INTEGER,
		ingredient_name TEXT NOT NULL, quantity_used REAL NOT NULL, unit TEXT NOT NULL DEFAULT '', cost_cents INTEGER);
	INSERT INTO recipes (id,name) VALUES (7,'soup');
	INSERT INTO meal_history (id,recipe_id,recipe_name,total_cost_cents) VALUES (5,7,'soup',123);
	INSERT INTO meal_history_ingredients (meal_history_id,ingredient_name,quantity_used) VALUES (5,'water',1);`)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()

	for i := 0; i < 2; i++ { // second open must be a no-op
		db, err := OpenDB(path)
		if err != nil {
			t.Fatal(err)
		}
		if notNull(t, db, "meal_history", "recipe_id") != 0 {
			t.Fatal("recipe_id still NOT NULL")
		}
		var id, rid, cost int
		if err := db.QueryRow(`SELECT id,recipe_id,total_cost_cents FROM meal_history`).Scan(&id, &rid, &cost); err != nil || id != 5 || rid != 7 || cost != 123 {
			t.Fatalf("row: %d %d %d %v", id, rid, cost, err)
		}
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM meal_history_ingredients WHERE meal_history_id=5`).Scan(&n)
		if n != 1 {
			t.Fatalf("ingredients %d", n)
		}
		db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='meal_history_recipe_idx'`).Scan(&n)
		if n != 1 {
			t.Fatal("index missing")
		}
		var fk int
		db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
		if fk != 1 {
			t.Fatal("foreign_keys off")
		}
		if i == 1 {
			if _, err := db.Exec(`UPDATE meal_history SET recipe_id=NULL`); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()
	}
}

func TestShoppingListSourcesMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a DB at the previous latest version without the table.
	if _, err := db.Exec(`DROP TABLE shopping_list_sources`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, LatestVersion()-2)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name='shopping_list_sources'`); n != 1 {
		t.Fatal("shopping_list_sources not created by migration")
	}
	// cascade delete works
	db.Exec(`INSERT INTO shopping_list (id,name) VALUES (1,'x')`)
	db.Exec(`INSERT INTO shopping_list_sources (shopping_id,source) VALUES (1,'recipe')`)
	if _, err := db.Exec(`DELETE FROM shopping_list WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM shopping_list_sources`); n != 0 {
		t.Fatalf("sources not cascaded: %d", n)
	}
}

func TestMealHistoryCalendarEntryColumnAndIndex(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if notNull(t, db, "meal_history", "calendar_entry_id") != 0 {
		t.Fatal("calendar_entry_id should be nullable")
	}
	if _, err := db.Exec(`INSERT INTO recipes (id,name) VALUES (1,'r')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meal_calendar (id,date,recipe_id) VALUES (1,'2024-01-01',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meal_history (recipe_name, calendar_entry_id) VALUES ('a',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meal_history (recipe_name, calendar_entry_id) VALUES ('b',1)`); err == nil {
		t.Fatal("expected unique violation")
	}
	for _, n := range []string{"c", "d"} {
		if _, err := db.Exec(`INSERT INTO meal_history (recipe_name) VALUES (?)`, n); err != nil {
			t.Fatal("multiple NULLs must be allowed:", err)
		}
	}
}

func TestNormalizeMealSlots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slots.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO recipes (id,name) VALUES (1,'r')`)
	for i, s := range []string{"Dinner", " LUNCH ", "snacks", "second breakfast"} {
		if _, err := db.Exec(`INSERT INTO meal_calendar (id,date,meal_slot,recipe_id) VALUES (?,?,?,1)`, i+1, "2024-01-01", s); err != nil {
			t.Fatal(err)
		}
	}
	// Roll back to before the slot migration so it runs again on reopen.
	if _, err := db.Exec(`PRAGMA user_version = ` + fmt.Sprint(LatestVersion()-1)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := []string{"dinner", "lunch", "snack", "second breakfast"}
	for i, w := range want {
		var got string
		db.QueryRow(`SELECT meal_slot FROM meal_calendar WHERE id=?`, i+1).Scan(&got)
		if got != w {
			t.Errorf("id %d: got %q want %q", i+1, got, w)
		}
	}
}
