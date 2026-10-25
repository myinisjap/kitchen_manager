package handlers_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/testutil"
)

func newMealsMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	db := testutil.NewDB(t)
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
	if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id,product_id,name,quantity,unit) VALUES (?,(SELECT product_id FROM stock_lots WHERE id=?),?,?,?)`, rid, invID, name, qty, unit); err != nil {
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
	if err := db.QueryRow(`SELECT quantity FROM stock_lots WHERE id=?`, id).Scan(&q); err != nil {
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
	db.QueryRow(`SELECT product_id, cost_cents FROM meal_history_ingredients`).Scan(&invID, &cost)
	if invID.Int64 != productOf(t, db, id) || cost.Valid {
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
	db.QueryRow(`SELECT COUNT(*) FROM stock_lots WHERE id=?`, a).Scan(&n)
	if n != 0 {
		t.Fatalf("emptied lot should be folded away")
	}
	var invID sql.NullInt64
	if err := db.QueryRow(`SELECT product_id FROM meal_history_ingredients`).Scan(&invID); err != nil {
		t.Fatal(err)
	}
	if !invID.Valid {
		t.Fatal("expected linked product id")
	}
	db.QueryRow(`SELECT COUNT(*) FROM products WHERE id=?`, invID.Int64).Scan(&n)
	if n != 1 {
		t.Fatalf("meal ingredient points at missing product %d", invID.Int64)
	}
}

func TestCookCostUsesConvertedQty(t *testing.T) {
	mux, db := newMealsMux(t)
	id := insertLot(t, db, "rice", 5000, "g", "", "")
	if _, err := db.Exec(`UPDATE stock_lots SET unit_cost_cents=2 WHERE id=?`, id); err != nil {
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

func newRecipe(t *testing.T, db *sql.DB, name string, servings int) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO recipes (name, servings) VALUES (?, ?) RETURNING id`, name, servings).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCookValidation(t *testing.T) {
	mux, _ := newMealsMux(t)
	if w := call(t, mux, "POST", "/api/meals", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("empty body: %d", w.Code)
	}
	if w := call(t, mux, "POST", "/api/meals", `{"recipe_id":9999}`); w.Code != http.StatusNotFound {
		t.Errorf("missing recipe: %d", w.Code)
	}
}

func TestCookDefaultsAndNotes(t *testing.T) {
	mux, db := newMealsMux(t)
	rid := newRecipe(t, db, "stew", 4)
	notes := strings.Repeat("n", 600)
	body := fmt.Sprintf(`{"recipe_id":%d,"cooked_at":"2024-03-05T12:00:00Z","notes":%q}`, rid, notes)
	if w := call(t, mux, "POST", "/api/meals", body); w.Code != http.StatusCreated {
		t.Fatalf("code %d: %s", w.Code, w.Body.String())
	}
	var servings int
	var cookedAt, gotNotes string
	if err := db.QueryRow(`SELECT servings_made, cooked_at, notes FROM meal_history WHERE recipe_id=?`, rid).Scan(&servings, &cookedAt, &gotNotes); err != nil {
		t.Fatal(err)
	}
	if servings != 4 {
		t.Errorf("servings_made %d, want recipe default 4", servings)
	}
	if cookedAt != "2024-03-05T12:00:00Z" {
		t.Errorf("cooked_at %q", cookedAt)
	}
	if len(gotNotes) != 500 {
		t.Errorf("notes len %d", len(gotNotes))
	}
}

func TestListMealsPagingAndFilter(t *testing.T) {
	mux, db := newMealsMux(t)
	a := newRecipe(t, db, "a", 1)
	b := newRecipe(t, db, "b", 1)
	for _, rid := range []int64{a, a, b} {
		if code := cook(t, mux, rid); code != http.StatusCreated {
			t.Fatalf("cook %d", code)
		}
	}
	get := func(q string) (int, int) {
		w := call(t, mux, "GET", "/api/meals"+q, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", q, w.Code)
		}
		var resp struct {
			Total int              `json:"total"`
			Meals []map[string]any `json:"meals"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Total, len(resp.Meals)
	}
	if tot, n := get(""); tot != 3 || n != 3 {
		t.Errorf("all: %d %d", tot, n)
	}
	if _, n := get("?limit=2"); n != 2 {
		t.Errorf("limit: %d", n)
	}
	if _, n := get("?offset=2"); n != 1 {
		t.Errorf("offset: %d", n)
	}
	if tot, n := get(fmt.Sprintf("?recipe_id=%d", a)); tot != 2 || n != 2 {
		t.Errorf("filter: %d %d", tot, n)
	}
	if _, n := get("?limit=500"); n != 3 {
		t.Errorf("big limit: %d", n)
	}
}

func TestMealStats(t *testing.T) {
	mux, db := newMealsMux(t)
	stats := func() map[string]any {
		w := call(t, mux, "GET", "/api/meals/stats", "")
		if w.Code != http.StatusOK {
			t.Fatalf("code %d", w.Code)
		}
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := stats()
	if m["total_spend_this_week_cents"] != nil || len(m["most_cooked"].([]any)) != 0 {
		t.Errorf("empty: %v", m)
	}

	a := newRecipe(t, db, "a", 1)
	b := newRecipe(t, db, "b", 1)
	now := time.Now().UTC().Format(time.RFC3339)
	old := time.Now().UTC().AddDate(0, 0, -60).Format(time.RFC3339)
	ins := func(rid int64, name, at string, cents int) {
		if _, err := db.Exec(`INSERT INTO meal_history (recipe_id, recipe_name, cooked_at, servings_made, total_cost_cents) VALUES (?,?,?,1,?)`, rid, name, at, cents); err != nil {
			t.Fatal(err)
		}
	}
	ins(a, "a", now, 100)
	ins(a, "a", now, 200)
	ins(b, "b", old, 1000)
	m = stats()
	if m["total_meals_this_week"].(float64) != 2 || m["total_spend_this_week_cents"].(float64) != 300 {
		t.Errorf("week: %v", m)
	}
	if m["total_meals_this_month"].(float64) != 2 {
		t.Errorf("month: %v", m)
	}
	mc := m["most_cooked"].([]any)
	if len(mc) != 2 || mc[0].(map[string]any)["recipe_name"] != "a" {
		t.Errorf("most_cooked: %v", mc)
	}
}

func TestMealStatsBounds(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	cases := []struct {
		name      string
		now       string
		loc       *time.Location
		wantWeek  string
		wantMonth string
	}{
		{"west", "2024-03-04T03:00:00Z", time.FixedZone("", -5*3600), "2024-02-26T05:00:00Z", "2024-03-01T05:00:00Z"},
		{"east", "2024-03-31T23:30:00Z", time.FixedZone("", 2*3600), "2024-03-31T22:00:00Z", "2024-03-31T22:00:00Z"},
		{"leap", "2024-02-29T20:00:00Z", time.FixedZone("", 9*3600), "2024-02-25T15:00:00Z", "2024-02-29T15:00:00Z"},
		{"dst", "2024-03-15T12:00:00Z", ny, "2024-03-11T04:00:00Z", "2024-03-01T05:00:00Z"},
	}
	for _, c := range cases {
		now, _ := time.Parse(time.RFC3339, c.now)
		w, m := handlers.MealStatsBounds(now, c.loc)
		if got := w.Format(time.RFC3339); got != c.wantWeek {
			t.Errorf("%s week %s want %s", c.name, got, c.wantWeek)
		}
		if got := m.Format(time.RFC3339); got != c.wantMonth {
			t.Errorf("%s month %s want %s", c.name, got, c.wantMonth)
		}
	}
}

func TestMealStatsTZBoundary(t *testing.T) {
	mux, db := newMealsMux(t)
	fixed, _ := time.Parse(time.RFC3339, "2024-07-01T06:00:00Z")
	defer handlers.SetMealsNow(func() time.Time { return fixed })()
	rid := newRecipe(t, db, "a", 1)
	for _, at := range []string{"2024-07-01T04:30:00Z", "2024-07-01T05:30:00Z"} {
		if _, err := db.Exec(`INSERT INTO meal_history (recipe_id, recipe_name, cooked_at, servings_made) VALUES (?,?,?,1)`, rid, "a", at); err != nil {
			t.Fatal(err)
		}
	}
	// offset-bearing POST is normalized to UTC and counted
	body := fmt.Sprintf(`{"recipe_id":%d,"cooked_at":"2024-07-01T00:40:00-05:00"}`, rid)
	if w := call(t, mux, "POST", "/api/meals", body); w.Code != http.StatusCreated {
		t.Fatalf("code %d: %s", w.Code, w.Body.String())
	}
	var stored string
	if err := db.QueryRow(`SELECT cooked_at FROM meal_history ORDER BY id DESC LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "2024-07-01T05:40:00Z" {
		t.Errorf("stored %q", stored)
	}
	for _, q := range []string{"tz_offset=-300", "tz=America/Chicago"} {
		w := call(t, mux, "GET", "/api/meals/stats?"+q, "")
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if m["total_meals_this_week"].(float64) != 2 || m["total_meals_this_month"].(float64) != 2 {
			t.Errorf("%s: %v", q, m)
		}
	}
}
