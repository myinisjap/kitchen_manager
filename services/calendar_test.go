package services_test

import (
	"database/sql"
	"math"
	"testing"

	"kitchen_manager/internal/testutil"
	"kitchen_manager/services"
)

func insertRecipe(t *testing.T, db *sql.DB, servings int) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO recipes (name, servings) VALUES ('r', ?) RETURNING id`, servings).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertIng(t *testing.T, db *sql.DB, recipeID int64, invID *int64, name string, qty float64, unit string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id, inventory_id, name, quantity, unit) VALUES (?,?,?,?,?)`,
		recipeID, invID, name, qty, unit); err != nil {
		t.Fatal(err)
	}
}

func schedule(t *testing.T, db *sql.DB, date string, recipeID int64, servings int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO meal_calendar (date, recipe_id, servings) VALUES (?,?,?)`, date, recipeID, servings); err != nil {
		t.Fatal(err)
	}
}

func byName(res []services.ShoppingNeed) map[string]services.ShoppingNeed {
	m := map[string]services.ShoppingNeed{}
	for _, r := range res {
		m[r.Name] = r
	}
	return m
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestWeeklyShoppingInvalidDate(t *testing.T) {
	db := testutil.NewDB(t)
	if _, err := services.GenerateWeeklyShopping(db, "2024/01/01"); err == nil {
		t.Fatal("expected error")
	}
}

func TestWeeklyShoppingRangeBoundaries(t *testing.T) {
	db := testutil.NewDB(t)
	r := insertRecipe(t, db, 1)
	insertIng(t, db, r, nil, "x", 1, "piece")
	for _, d := range []string{"2023-12-31", "2024-01-08"} {
		schedule(t, db, d, r, 1)
	}
	res, err := services.GenerateWeeklyShopping(db, "2024-01-01")
	if err != nil || len(res) != 0 {
		t.Fatalf("outside: %v %v", res, err)
	}
	schedule(t, db, "2024-01-01", r, 1)
	schedule(t, db, "2024-01-07", r, 1)
	res, _ = services.GenerateWeeklyShopping(db, "2024-01-01")
	if len(res) != 1 || !near(res[0].QuantityNeeded, 2) {
		t.Fatalf("inside: %+v", res)
	}
}

func TestWeeklyShoppingScalingAndSums(t *testing.T) {
	db := testutil.NewDB(t)
	inv := insertInv(t, db, "flour", 50, "g", "", 0)
	r := insertRecipe(t, db, 2)
	insertIng(t, db, r, &inv, "flour", 100, "g")
	schedule(t, db, "2024-01-01", r, 4)
	res, err := services.GenerateWeeklyShopping(db, "2024-01-01")
	if err != nil || len(res) != 1 || !near(res[0].QuantityNeeded, 150) || res[0].InventoryID == nil || *res[0].InventoryID != inv {
		t.Fatalf("%+v %v", res, err)
	}
	// second entry sums before inventory is subtracted once
	schedule(t, db, "2024-01-02", r, 2)
	res, _ = services.GenerateWeeklyShopping(db, "2024-01-01")
	if len(res) != 1 || !near(res[0].QuantityNeeded, 250) {
		t.Fatalf("sum: %+v", res)
	}
}

func TestWeeklyShoppingZeroRecipeServingsAndCovered(t *testing.T) {
	db := testutil.NewDB(t)
	inv := insertInv(t, db, "rice", 1000, "g", "", 0)
	r := insertRecipe(t, db, 0)
	insertIng(t, db, r, &inv, "rice", 100, "g")
	schedule(t, db, "2024-01-01", r, 3)
	res, err := services.GenerateWeeklyShopping(db, "2024-01-01")
	if err != nil || len(res) != 0 {
		t.Fatalf("covered: %+v %v", res, err)
	}
	if _, err := db.Exec(`UPDATE inventory SET quantity=0`); err != nil {
		t.Fatal(err)
	}
	res, _ = services.GenerateWeeklyShopping(db, "2024-01-01")
	if len(res) != 1 || !near(res[0].QuantityNeeded, 300) {
		t.Fatalf("servings 0 treated as 1: %+v", res)
	}
}

func TestWeeklyShoppingUnits(t *testing.T) {
	db := testutil.NewDB(t)
	r := insertRecipe(t, db, 1)
	kg := insertInv(t, db, "pref", 0, "kg", "g", 0)
	plain := insertInv(t, db, "plain", 0, "kg", "", 0)
	bad := insertInv(t, db, "bad", 0, "g", "", 0)
	insertIng(t, db, r, &kg, "pref", 1, "kg")
	insertIng(t, db, r, &plain, "plain", 1, "kg")
	// incompatible units: need reported in the recipe unit
	insertIng(t, db, r, &bad, "bad", 2, "cup")
	insertIng(t, db, r, nil, "unlinked", 3, "tbsp")
	schedule(t, db, "2024-01-01", r, 1)
	res, err := services.GenerateWeeklyShopping(db, "2024-01-01")
	if err != nil {
		t.Fatal(err)
	}
	m := byName(res)
	if g := m["pref"]; g.Unit != "g" || !near(g.QuantityNeeded, 1000) {
		t.Errorf("pref: %+v", g)
	}
	if g := m["plain"]; g.Unit != "kg" || !near(g.QuantityNeeded, 1) {
		t.Errorf("plain: %+v", g)
	}
	if g := m["bad"]; g.Unit != "cup" || !near(g.QuantityNeeded, 2) {
		t.Errorf("bad: %+v", g)
	}
	if g := m["unlinked"]; g.InventoryID != nil || g.Unit != "tbsp" || !near(g.QuantityNeeded, 3) {
		t.Errorf("unlinked: %+v", g)
	}
}

func TestWeeklyShoppingIncompatibleUnitsIgnoreStock(t *testing.T) {
	db := testutil.NewDB(t)
	r := insertRecipe(t, db, 1)
	sugar := insertInv(t, db, "sugar", 500, "g", "", 0)
	insertIng(t, db, r, &sugar, "sugar", 2, "cup")
	schedule(t, db, "2024-01-01", r, 1)
	res, err := services.GenerateWeeklyShopping(db, "2024-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 need, got %+v", res)
	}
	if g := res[0]; g.Unit != "cup" || !near(g.QuantityNeeded, 2) || g.InventoryID == nil || *g.InventoryID != sugar {
		t.Errorf("sugar: %+v", g)
	}
}

func TestWeeklyShoppingSkipsCookedEntries(t *testing.T) {
	db := testutil.NewDB(t)
	inv := insertInv(t, db, "flour", 0, "g", "", 0)
	r := insertRecipe(t, db, 1)
	insertIng(t, db, r, &inv, "flour", 100, "g")
	schedule(t, db, "2024-01-01", r, 1)
	schedule(t, db, "2024-01-02", r, 1)
	var first int64
	db.QueryRow(`SELECT MIN(id) FROM meal_calendar`).Scan(&first)
	if _, err := db.Exec(`INSERT INTO meal_history (recipe_id, recipe_name, calendar_entry_id) VALUES (?, 'r', ?)`, r, first); err != nil {
		t.Fatal(err)
	}
	res, err := services.GenerateWeeklyShopping(db, "2024-01-01")
	if err != nil || len(res) != 1 || !near(res[0].QuantityNeeded, 100) {
		t.Fatalf("%+v %v", res, err)
	}
}
