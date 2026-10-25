package handlers_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/testutil"
)

func newAllMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterProducts(mux, db)
	handlers.RegisterHistory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterMeals(mux, db)
	handlers.RegisterCalendar(mux, db)
	return mux, db
}

func decodeMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return m
}

// Stock split over several lots counts as one total: each lot alone is short
// of the recipe, together they cover it.
func TestRecipeAvailableWhenStockSplitOverLots(t *testing.T) {
	mux, db := newAllMux(t)
	_, a := testutil.AddLot(t, db, "Flour", 300, "g")
	testutil.AddLot(t, db, "Flour", 0.5, "kg", testutil.WithLocation("Cellar"))
	rid := recipeWith(t, db, a, "Flour", 700, "g")

	w := call(t, mux, "GET", "/api/recipes/?available_only=true", "")
	var recipes []map[string]any
	json.Unmarshal(w.Body.Bytes(), &recipes)
	if len(recipes) != 1 || int64(recipes[0]["id"].(float64)) != rid {
		t.Fatalf("recipe should be available: %s", w.Body.String())
	}
	// More than the 800 g in stock: no longer available.
	db.Exec(`UPDATE recipe_ingredients SET quantity=900`)
	w = call(t, mux, "GET", "/api/recipes/?available_only=true", "")
	json.Unmarshal(w.Body.Bytes(), &recipes)
	if len(recipes) != 0 {
		t.Fatalf("should not be available: %s", w.Body.String())
	}
}

func TestAddToShoppingListUsesTotalAcrossLots(t *testing.T) {
	mux, db := newAllMux(t)
	_, a := testutil.AddLot(t, db, "Flour", 300, "g")
	testutil.AddLot(t, db, "Flour", 200, "g", testutil.WithLocation("Cellar"))
	rid := recipeWith(t, db, a, "Flour", 700, "g")
	w := call(t, mux, "POST", fmt.Sprintf("/api/recipes/%d/add-to-shopping-list", rid), `{}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var qty float64
	var pid int64
	if err := db.QueryRow(`SELECT quantity_needed, product_id FROM shopping_list`).Scan(&qty, &pid); err != nil {
		t.Fatal(err)
	}
	if qty != 200 || pid != productOf(t, db, a) {
		t.Fatalf("shortfall %v product %d", qty, pid)
	}
}

func TestWeeklyShoppingUsesTotalAcrossLots(t *testing.T) {
	mux, db := newAllMux(t)
	_, a := testutil.AddLot(t, db, "Pasta", 300, "g")
	testutil.AddLot(t, db, "Pasta", 400, "g", testutil.WithLocation("Cellar"))
	rid := recipeWith(t, db, a, "Pasta", 600, "g")
	db.Exec(`INSERT INTO meal_calendar (date,meal_slot,recipe_id,servings) VALUES ('2026-05-04','dinner',?,1)`, rid)
	w := call(t, mux, "POST", "/api/calendar/generate-weekly-shopping", `{"week_start":"2026-05-04"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM shopping_list`); n != 0 {
		t.Fatalf("700 g in stock covers 600 g, got %d shopping rows: %s", n, w.Body.String())
	}
}

func TestCookDeductsAcrossLotsInDifferentUnits(t *testing.T) {
	mux, db := newMealsMux(t)
	_, g := testutil.AddLot(t, db, "Sugar", 200, "g", testutil.WithExpiry("2026-05-01"))
	_, kg := testutil.AddLot(t, db, "Sugar", 1, "kg", testutil.WithExpiry("2026-06-01"), testutil.WithPreferredUnit("g"))
	rid := recipeWith(t, db, g, "Sugar", 500, "g")
	if code := cook(t, mux, rid); code != http.StatusCreated {
		t.Fatalf("code %d", code)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM stock_lots WHERE id=?`, g); n != 0 {
		t.Fatal("emptied gram lot should be gone")
	}
	if q := qtyOf(t, db, kg); !near(q, 0.7) {
		t.Fatalf("kg lot has %v, want 0.7", q)
	}
}

func TestCookWithoutAnyLotsKeepsIngredientLink(t *testing.T) {
	mux, db := newMealsMux(t)
	pid, lot := testutil.AddLot(t, db, "Salt", 5, "g")
	rid := recipeWith(t, db, lot, "Salt", 5, "g")
	if code := cook(t, mux, rid); code != http.StatusCreated {
		t.Fatalf("code %d", code)
	}
	// Cooking again with no stock left must still work and keep the link.
	if code := cook(t, mux, rid); code != http.StatusCreated {
		t.Fatalf("second cook code %d", code)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM meal_history_ingredients WHERE product_id=?`, pid); n != 2 {
		t.Fatalf("meal ingredients linked to product: %d", n)
	}
}

func TestRenameLotToExistingProductMovesIt(t *testing.T) {
	mux, db := newAllMux(t)
	_, milk := testutil.AddLot(t, db, "Milk", 1, "L", testutil.WithPar(2))
	_, other := testutil.AddLot(t, db, "Whole Milk", 2, "L")
	w := call(t, mux, "PATCH", fmt.Sprintf("/api/inventory/%d", other), `{"name":"milk"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if productOf(t, db, other) != productOf(t, db, milk) {
		t.Fatal("lot not moved to the existing product")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM products`); n != 1 {
		t.Fatalf("empty product should be folded away: %d", n)
	}
}

func TestRenameOneOfSeveralLotsCreatesProduct(t *testing.T) {
	mux, db := newAllMux(t)
	_, a := testutil.AddLot(t, db, "Rice", 1, "kg", testutil.WithPar(3))
	_, b := testutil.AddLot(t, db, "Rice", 2, "kg", testutil.WithLocation("Cellar"))
	w := call(t, mux, "PATCH", fmt.Sprintf("/api/inventory/%d", b), `{"name":"Basmati"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if productOf(t, db, a) == productOf(t, db, b) {
		t.Fatal("renamed lot should be a new product")
	}
	var par float64
	db.QueryRow(`SELECT par_level FROM products WHERE id=?`, productOf(t, db, b)).Scan(&par)
	if par != 3 {
		t.Fatalf("new product should inherit par, got %v", par)
	}
}

func TestLegacyInventoryIDInRecipeMapsToProduct(t *testing.T) {
	mux, db := newAllMux(t)
	pid, lot := testutil.AddLot(t, db, "Flour", 500, "g")
	testutil.AddLot(t, db, "Flour", 100, "g", testutil.WithLocation("Cellar"))
	body := fmt.Sprintf(`{"name":"Bread","servings":1,"ingredients":[{"name":"anything","quantity":100,"unit":"g","inventory_id":%d}]}`, lot)
	w := call(t, mux, "POST", "/api/recipes/", body)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	ing := decodeMap(t, w.Body.Bytes())["ingredients"].([]any)[0].(map[string]any)
	if int64(ing["product_id"].(float64)) != pid {
		t.Fatalf("ingredient not linked to product %d: %v", pid, ing)
	}
	if _, has := ing["inventory_id"]; has {
		t.Fatal("recipe ingredients should no longer expose inventory_id")
	}
	// Unlinked ingredient resolves by name (case-insensitive).
	w = call(t, mux, "POST", "/api/recipes/", `{"name":"Roll","servings":1,"ingredients":[{"name":"flour","quantity":1,"unit":"g"}]}`)
	ing = decodeMap(t, w.Body.Bytes())["ingredients"].([]any)[0].(map[string]any)
	if int64(ing["product_id"].(float64)) != pid {
		t.Fatalf("name did not resolve to product: %v", ing)
	}
}

func TestBarcodeLookupForProductWithNoLots(t *testing.T) {
	mux, db := newAllMux(t)
	pid, lot := testutil.AddLot(t, db, "Olive Oil", 1, "L", testutil.WithBarcode("5000"), testutil.WithPar(1))
	db.Exec(`DELETE FROM stock_lots WHERE id=?`, lot)
	w := call(t, mux, "GET", "/api/inventory/barcode/5000", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	m := decodeMap(t, w.Body.Bytes())
	if m["id"].(float64) != 0 || int64(m["product_id"].(float64)) != pid || m["name"] != "Olive Oil" || m["quantity"].(float64) != 0 {
		t.Fatalf("stub: %v", m)
	}
	// Posting the same barcode adds a lot to the existing product.
	w = call(t, mux, "POST", "/api/inventory/", `{"name":"Oil","quantity":2,"unit":"L","barcode":"5000"}`)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM products`); n != 1 {
		t.Fatalf("products=%d", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM stock_lots WHERE product_id=?`, pid); n != 1 {
		t.Fatal("lot not added to existing product")
	}
}

func TestGroupedMergesLotsInDifferentUnits(t *testing.T) {
	mux, db := newAllMux(t)
	testutil.AddLot(t, db, "Flour", 500, "g", testutil.WithPar(2000))
	testutil.AddLot(t, db, "Flour", 1, "kg", testutil.WithLocation("Cellar"))
	testutil.AddLot(t, db, "Eggs", 6, "piece")
	testutil.AddLot(t, db, "Eggs", 100, "g")
	w := call(t, mux, "GET", "/api/inventory/grouped", "")
	var groups []map[string]any
	json.Unmarshal(w.Body.Bytes(), &groups)
	if len(groups) != 2 {
		t.Fatalf("want one group per product, got %s", w.Body.String())
	}
	var flour, eggs map[string]any
	for _, g := range groups {
		switch g["name"] {
		case "Flour":
			flour = g
		case "Eggs":
			eggs = g
		}
	}
	if flour["total_quantity"].(float64) != 1500 || flour["unit"] != "g" || flour["below_par"] != true {
		t.Fatalf("flour group: %v", flour)
	}
	mism := 0
	for _, l := range eggs["locations"].([]any) {
		if l.(map[string]any)["unit_mismatch"] == true {
			mism++
		}
	}
	if eggs["total_quantity"].(float64) != 6 || mism != 1 {
		t.Fatalf("eggs group: %v", eggs)
	}
}

func TestDeductCascadesAcrossProductLotsAndLeavesStub(t *testing.T) {
	mux, db := newAllMux(t)
	pid, a := testutil.AddLot(t, db, "Butter", 100, "g")
	testutil.AddLot(t, db, "Butter", 100, "g", testutil.WithLocation("Cellar"))
	w := call(t, mux, "POST", fmt.Sprintf("/api/inventory/%d/deduct", a), `{"quantity":250}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	m := decodeMap(t, w.Body.Bytes())
	if m["id"].(float64) != 0 || m["deducted"].(float64) != 200 || m["quantity"].(float64) != 0 || int64(m["product_id"].(float64)) != pid {
		t.Fatalf("%v", m)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM products WHERE id=?`, pid) != 1 {
		t.Fatal("product should remain after its stock ran out")
	}
}
