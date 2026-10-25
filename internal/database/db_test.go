package database

import "testing"

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
