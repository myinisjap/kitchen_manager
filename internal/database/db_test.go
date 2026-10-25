package database

import (
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
