package handlers_test

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/database"
)

func newMealsMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	db, err := database.OpenDB(filepath.Join(t.TempDir(), "kitchen.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterMeals(mux, db)
	return mux, db
}

func recipeWith(t *testing.T, db *sql.DB, invID int64, name string, qty float64, unit string) int64 {
	t.Helper()
	var rid int64
	if err := db.QueryRow(`INSERT INTO recipes (name, servings) VALUES ('r', 1) RETURNING id`).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id,inventory_id,name,quantity,unit) VALUES (?,?,?,?,?)`, rid, invID, name, qty, unit); err != nil {
		t.Fatal(err)
	}
	return rid
}

func cook(t *testing.T, mux *http.ServeMux, rid int64) int {
	t.Helper()
	return call(t, mux, "POST", "/api/meals", fmt.Sprintf(`{"recipe_id":%d}`, rid)).Code
}

func qtyOf(t *testing.T, db *sql.DB, id int64) float64 {
	t.Helper()
	var q float64
	if err := db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, id).Scan(&q); err != nil {
		t.Fatal(err)
	}
	return q
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestCookConvertsVolumeUnits(t *testing.T) {
	mux, db := newMealsMux(t)
	id := insertLot(t, db, "milk", 1000, "ml", "", "")
	rid := recipeWith(t, db, id, "milk", 2, "cup")
	if code := cook(t, mux, rid); code != http.StatusCreated {
		t.Fatalf("code %d", code)
	}
	if q := qtyOf(t, db, id); !near(q, 1000-473.176) {
		t.Fatalf("qty %v", q)
	}
	var unit, source string
	if err := db.QueryRow(`SELECT unit, source FROM inventory_history WHERE inventory_id=?`, id).Scan(&unit, &source); err != nil {
		t.Fatal(err)
	}
	if unit != "ml" || source != "meal_cooked" {
		t.Fatalf("history %q %q", unit, source)
	}
}

func TestCookConvertsMass(t *testing.T) {
	mux, db := newMealsMux(t)
	id := insertLot(t, db, "sugar", 500, "g", "", "")
	rid := recipeWith(t, db, id, "sugar", 1, "oz")
	if code := cook(t, mux, rid); code != http.StatusCreated {
		t.Fatalf("code %d", code)
	}
	if q := qtyOf(t, db, id); !near(q, 500-28.3495) {
		t.Fatalf("qty %v", q)
	}
}

func TestCookIncompatibleUnitsWarns(t *testing.T) {
	mux, db := newMealsMux(t)
	id := insertLot(t, db, "flour", 500, "g", "", "")
	rid := recipeWith(t, db, id, "flour", 2, "cup")
	w := call(t, mux, "POST", "/api/meals", fmt.Sprintf(`{"recipe_id":%d}`, rid))
	if w.Code != http.StatusCreated {
		t.Fatalf("code %d", w.Code)
	}
	if q := qtyOf(t, db, id); q != 500 {
		t.Fatalf("qty %v", q)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM meal_history`).Scan(&n)
	if n != 1 {
		t.Fatalf("meal rows %d", n)
	}
	var invID sql.NullInt64
	var cost sql.NullInt64
	db.QueryRow(`SELECT inventory_id, cost_cents FROM meal_history_ingredients`).Scan(&invID, &cost)
	if invID.Int64 != id || cost.Valid {
		t.Fatalf("ingredient row %v %v", invID, cost)
	}
	want := "Couldn't deduct 2 cup of flour: stock is tracked in g"
	if !strings.Contains(w.Body.String(), want) {
		t.Fatalf("missing warning: %s", w.Body.String())
	}
}

func TestCookCascadesAcrossLots(t *testing.T) {
	mux, db := newMealsMux(t)
	a := insertLot(t, db, "stock", 100, "ml", "2026-05-01", "")
	b := insertLot(t, db, "stock", 300, "ml", "2026-06-01", "")
	c := insertLot(t, db, "stock", 500, "ml", "", "")
	rid := recipeWith(t, db, c, "stock", 0.25, "L")
	if code := cook(t, mux, rid); code != http.StatusCreated {
		t.Fatalf("code %d", code)
	}
	if q := qtyOf(t, db, b); !near(q, 150) {
		t.Fatalf("b %v", q)
	}
	if q := qtyOf(t, db, c); q != 500 {
		t.Fatalf("c %v", q)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE id=?`, a).Scan(&n)
	if n != 0 {
		t.Fatalf("emptied lot should be folded away")
	}
	var invID sql.NullInt64
	if err := db.QueryRow(`SELECT inventory_id FROM meal_history_ingredients`).Scan(&invID); err != nil {
		t.Fatal(err)
	}
	if !invID.Valid {
		t.Fatal("expected linked inventory id")
	}
	db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE id=?`, invID.Int64).Scan(&n)
	if n != 1 {
		t.Fatalf("meal ingredient points at missing row %d", invID.Int64)
	}
}

func TestCookCostUsesConvertedQty(t *testing.T) {
	mux, db := newMealsMux(t)
	id := insertLot(t, db, "rice", 5000, "g", "", "")
	if _, err := db.Exec(`UPDATE inventory SET unit_cost_cents=2 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	rid := recipeWith(t, db, id, "rice", 1, "kg")
	if code := cook(t, mux, rid); code != http.StatusCreated {
		t.Fatalf("code %d", code)
	}
	var cost int64
	if err := db.QueryRow(`SELECT total_cost_cents FROM meal_history`).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost != 2000 {
		t.Fatalf("cost %d", cost)
	}
}

func TestCookCountUnits(t *testing.T) {
	mux, db := newMealsMux(t)
	id := insertLot(t, db, "egg", 12, "piece", "", "")
	rid := recipeWith(t, db, id, "egg", 3, "piece")
	if code := cook(t, mux, rid); code != http.StatusCreated {
		t.Fatalf("code %d", code)
	}
	if q := qtyOf(t, db, id); q != 9 {
		t.Fatalf("qty %v", q)
	}
}
