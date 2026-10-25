package database

import (
	"database/sql"

	"path/filepath"
	"testing"
)

func TestOpenDBSchema(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, tbl := range []string{"inventory", "recipes", "recipe_ingredients", "shopping_list", "meal_calendar", "meal_history", "inventory_history", "settings", "sessions"} {
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

func TestNormalizeMealCookedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO recipes (name) VALUES ('r')`); err != nil {
		t.Fatal(err)
	}
	in := []string{"2024-03-05T07:00:00-05:00", "2024-03-05 12:00:00", "2024-03-05T12:00:00.123Z", "2024-03-05T12:00:00Z", "garbage"}
	for _, v := range in {
		if _, err := db.Exec(`INSERT INTO meal_history (recipe_id, recipe_name, cooked_at, servings_made) VALUES (1,'r',?,1)`, v); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	for i := 0; i < 2; i++ {
		db, err = OpenDB(path)
		if err != nil {
			t.Fatal(err)
		}
		for j := range in {
			var got string
			if err := db.QueryRow(`SELECT cooked_at FROM meal_history WHERE id=?`, j+1).Scan(&got); err != nil {
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
