package handlers_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// mergeSetup: inventory pasta 500g, recipe "Pasta Bake" (300g pasta, 1 serving)
// scheduled on two days of week 2026-04-20.
func mergeSetup(t *testing.T, db *sql.DB, days ...string) (invID, recipeID int64) {
	t.Helper()
	invID = insertLot(t, db, "pasta", 500, "g", "", "")
	if err := db.QueryRow(`INSERT INTO recipes (name,servings) VALUES ('Pasta Bake',1) RETURNING id`).Scan(&recipeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id,product_id,name,quantity,unit) VALUES (?,(SELECT product_id FROM stock_lots WHERE id=?),?,?,?)`,
		recipeID, invID, "pasta", 300, "g"); err != nil {
		t.Fatal(err)
	}
	for _, d := range days {
		if _, err := db.Exec(`INSERT INTO meal_calendar (date,meal_slot,recipe_id,servings) VALUES (?,?,?,1)`, d, "dinner", recipeID); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func getList(t *testing.T, mux interface{}, call func(string, string, string) []byte) []map[string]any {
	t.Helper()
	var items []map[string]any
	if err := json.Unmarshal(call("GET", "/api/shopping/?show_checked=true", ""), &items); err != nil {
		t.Fatal(err)
	}
	return items
}

func TestMergeScenarios(t *testing.T) {
	mux, db := newMux(t)
	do := func(m, p, b string) []byte { return call(t, mux, m, p, b).Body.Bytes() }
	list := func() []map[string]any { return getList(t, mux, do) }
	_, rid := mergeSetup(t, db, "2026-04-20", "2026-04-22")
	gen := "/api/calendar/generate-weekly-shopping?start=2026-04-20"

	// 1. weekly idempotent
	do("POST", gen, "")
	do("POST", gen, "")
	items := list()
	if len(items) != 1 || items[0]["quantity_needed"].(float64) != 100 {
		t.Fatalf("want one pasta row of 100, got %v", items)
	}
	srcs := items[0]["sources"].([]any)
	if len(srcs) != 1 || !strings.Contains(srcs[0].(map[string]any)["label"].(string), "Pasta Bake") {
		t.Fatalf("bad sources: %v", srcs)
	}

	// 4. recipe merges into same row; sources has both
	// 3. recipe twice: shortfall 300-500 <0 → nothing. Lower stock first.
	db.Exec(`UPDATE stock_lots SET quantity=100`)
	rpath := fmt.Sprintf("/api/recipes/%d/add-to-shopping-list", rid)
	var r1, r2 map[string]any
	json.Unmarshal(do("POST", rpath, ""), &r1)
	json.Unmarshal(do("POST", rpath, ""), &r2)
	if r1["added"].(float64) != 0 || r1["merged"].(float64) != 1 || r2["merged"].(float64) != 1 {
		t.Fatalf("recipe responses: %v %v", r1, r2)
	}
	items = list()
	if len(items) != 1 || items[0]["quantity_needed"].(float64) != 100+200+200 {
		t.Fatalf("want one row of 500, got %v", items)
	}
	if len(items[0]["sources"].([]any)) != 2 {
		t.Fatalf("want 2 sources: %v", items[0]["sources"])
	}

	// 5. checked rows are not merge targets
	id := int64(items[0]["id"].(float64))
	do("PATCH", fmt.Sprintf("/api/shopping/%d", id), `{"checked":true}`)
	do("POST", rpath, "")
	if items = list(); len(items) != 2 {
		t.Fatalf("want new unchecked row, got %v", items)
	}

	// 8. deletes succeed with sources present
	if w := call(t, mux, "DELETE", "/api/shopping/checked", ""); w.Code != 200 {
		t.Fatalf("delete checked: %d %s", w.Code, w.Body)
	}
	items = list()
	if len(items) != 1 {
		t.Fatalf("got %v", items)
	}
	if w := call(t, mux, "DELETE", fmt.Sprintf("/api/shopping/%d", int64(items[0]["id"].(float64))), ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM shopping_list_sources`).Scan(&n)
	if n != 0 {
		t.Fatalf("orphan sources: %d", n)
	}
}

func TestWeeklyMergeUnitConversion(t *testing.T) {
	mux, db := newMux(t)
	do := func(m, p, b string) []byte { return call(t, mux, m, p, b).Body.Bytes() }
	mergeSetup(t, db, "2026-04-20", "2026-04-22")
	do("POST", "/api/shopping/", `{"name":"Pasta","quantity_needed":1,"unit":"kg"}`)
	gen := "/api/calendar/generate-weekly-shopping?start=2026-04-20"
	for i := 0; i < 2; i++ {
		do("POST", gen, "")
		items := getList(t, mux, do)
		if len(items) != 1 || items[0]["unit"] != "kg" || items[0]["quantity_needed"].(float64) != 1.1 {
			t.Fatalf("run %d: %v", i, items)
		}
	}
}

func TestMergeIncompatibleUnitsAndNames(t *testing.T) {
	mux, db := newMux(t)
	do := func(m, p, b string) []byte { return call(t, mux, m, p, b).Body.Bytes() }
	mergeSetup(t, db, "2026-04-20", "2026-04-22")
	do("POST", "/api/shopping/", `{"name":"pasta","quantity_needed":1,"unit":"piece"}`)
	do("POST", "/api/calendar/generate-weekly-shopping?start=2026-04-20", "")
	if items := getList(t, mux, do); len(items) != 2 {
		t.Fatalf("incompatible units should not merge: %v", items)
	}

	// name normalisation for unlinked ingredients
	var rid int64
	db.QueryRow(`INSERT INTO recipes (name,servings) VALUES ('Aioli',1) RETURNING id`).Scan(&rid)
	db.Exec(`INSERT INTO recipe_ingredients (recipe_id,name,quantity,unit) VALUES (?,?,?,?)`, rid, "Garlic ", 2, "clove")
	do("POST", "/api/shopping/", `{"name":"garlic","quantity_needed":1,"unit":"clove"}`)
	do("POST", fmt.Sprintf("/api/recipes/%d/add-to-shopping-list", rid), "")
	n := 0
	for _, it := range getList(t, mux, do) {
		if strings.EqualFold(strings.TrimSpace(it["name"].(string)), "garlic") {
			n++
			if it["quantity_needed"].(float64) != 3 {
				t.Fatalf("garlic qty: %v", it)
			}
		}
	}
	if n != 1 {
		t.Fatalf("want 1 garlic row, got %d", n)
	}
}
