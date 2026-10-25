package handlers_test

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/testutil"
)

// newSchemaMux builds handlers on a database created by the real schema code,
// so foreign keys and migrations match production.
func newSchemaMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	return mux, db
}

func call(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func insertLot(t *testing.T, db *sql.DB, name string, qty float64, unit, expiry, barcode string) int64 {
	t.Helper()
	_, id := testutil.AddLot(t, db, name, qty, unit, testutil.WithExpiry(expiry), testutil.WithBarcode(barcode), testutil.WithPar(1))
	return id
}

func linkRecipe(t *testing.T, db *sql.DB, invID int64) int64 {
	t.Helper()
	var recipeID int64
	if err := db.QueryRow(`INSERT INTO recipes (name) VALUES ('bread') RETURNING id`).Scan(&recipeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id,product_id,name,quantity,unit) VALUES (?,(SELECT product_id FROM stock_lots WHERE id=?),'flour',100,'g')`, recipeID, invID); err != nil {
		t.Fatal(err)
	}
	return recipeID
}

func ingredientProductID(t *testing.T, db *sql.DB, recipeID int64) sql.NullInt64 {
	t.Helper()
	var pid sql.NullInt64
	if err := db.QueryRow(`SELECT product_id FROM recipe_ingredients WHERE recipe_id=?`, recipeID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return pid
}

func productOf(t *testing.T, db *sql.DB, lot int64) int64 {
	t.Helper()
	var pid int64
	if err := db.QueryRow(`SELECT product_id FROM stock_lots WHERE id=?`, lot).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return pid
}

// Emptying the last lot removes the lot but keeps the product: its recipe
// link and par level survive, and it still reaches the shopping list.
func TestDeductLinkedItemToZeroKeepsProduct(t *testing.T) {
	mux, db := newSchemaMux(t)
	id := insertLot(t, db, "flour", 500, "g", "2026-12-01", "FLOUR1")
	pid := productOf(t, db, id)
	recipeID := linkRecipe(t, db, id)

	w := call(t, mux, http.MethodPatch, "/api/inventory/"+strconv.FormatInt(id, 10), `{"quantity":0}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM stock_lots WHERE product_id=?`, pid); n != 0 {
		t.Errorf("emptied lot should be deleted, %d lots left", n)
	}
	var par float64
	if err := db.QueryRow(`SELECT par_level FROM products WHERE id=?`, pid).Scan(&par); err != nil || par != 1 {
		t.Errorf("product/par lost: %v %v", par, err)
	}
	if got := ingredientProductID(t, db, recipeID); !got.Valid || got.Int64 != pid {
		t.Errorf("recipe link changed: %+v", got)
	}

	// Out-of-stock products must still reach the shopping list.
	w = call(t, mux, http.MethodPost, "/api/shopping/generate-from-thresholds", "")
	if w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM shopping_list WHERE product_id=?`, pid); n != 1 {
		t.Errorf("expected out-of-stock product on shopping list, got %d rows", n)
	}
}

func TestDeductEmptyLotKeepsSiblingAndProductLinks(t *testing.T) {
	mux, db := newSchemaMux(t)
	first := insertLot(t, db, "flour", 200, "g", "2026-11-01", "FLOUR1")
	second := insertLot(t, db, "flour", 500, "g", "2026-12-01", "")
	pid := productOf(t, db, first)
	recipeID := linkRecipe(t, db, first)
	if _, err := db.Exec(`INSERT INTO product_skus (product_id, barcode, quantity_per_scan) VALUES (?, 'FLOUR-ALIAS', 1)`, pid); err != nil {
		t.Fatal(err)
	}

	w := call(t, mux, http.MethodPatch, "/api/inventory/"+strconv.FormatInt(first, 10)+"?source=manual", `{"quantity":0}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM stock_lots WHERE id=?`, first); n != 0 {
		t.Errorf("expected emptied lot to be removed")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM stock_lots WHERE id=?`, second); n != 1 {
		t.Errorf("sibling lot should remain")
	}
	if got := ingredientProductID(t, db, recipeID); !got.Valid || got.Int64 != pid {
		t.Errorf("recipe should still link to product %d, got %+v", pid, got)
	}
	var barcode string
	db.QueryRow(`SELECT barcode FROM products WHERE id=?`, pid).Scan(&barcode)
	if barcode != "FLOUR1" {
		t.Errorf("primary barcode should stay on the product, got %q", barcode)
	}
	var aliasOwner int64
	db.QueryRow(`SELECT product_id FROM product_skus WHERE barcode='FLOUR-ALIAS'`).Scan(&aliasOwner)
	if aliasOwner != pid {
		t.Errorf("SKU alias should stay on the product, owner=%d", aliasOwner)
	}
}

func TestDeleteLastLotRemovesProductAndClearsLinks(t *testing.T) {
	mux, db := newSchemaMux(t)
	id := insertLot(t, db, "flour", 500, "g", "", "")
	pid := productOf(t, db, id)
	recipeID := linkRecipe(t, db, id)
	if _, err := db.Exec(`INSERT INTO shopping_list (product_id,name,quantity_needed,unit) VALUES (?, 'flour', 1, 'g')`, pid); err != nil {
		t.Fatal(err)
	}

	w := call(t, mux, http.MethodDelete, "/api/inventory/"+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if got := ingredientProductID(t, db, recipeID); got.Valid {
		t.Errorf("expected recipe link cleared, got %+v", got)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM shopping_list WHERE product_id IS NOT NULL`); n != 0 {
		t.Errorf("shopping link not cleared")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM products`); n != 0 {
		t.Errorf("product should be deleted with its last lot")
	}

	w = call(t, mux, http.MethodDelete, "/api/inventory/"+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing item, got %d", w.Code)
	}
}

func TestDeleteOneOfSeveralLotsKeepsLinks(t *testing.T) {
	mux, db := newSchemaMux(t)
	id := insertLot(t, db, "flour", 500, "g", "2026-11-01", "")
	insertLot(t, db, "flour", 300, "g", "2026-12-01", "")
	pid := productOf(t, db, id)
	recipeID := linkRecipe(t, db, id)

	w := call(t, mux, http.MethodDelete, "/api/inventory/"+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if got := ingredientProductID(t, db, recipeID); !got.Valid || got.Int64 != pid {
		t.Errorf("expected recipe to keep product %d, got %+v", pid, got)
	}
}

func TestMergeLinkedItem(t *testing.T) {
	mux, db := newSchemaMux(t)
	source := insertLot(t, db, "flour", 200, "g", "", "")
	target := insertLot(t, db, "all purpose flour", 500, "g", "", "")
	recipeID := linkRecipe(t, db, source)

	path := "/api/inventory/" + strconv.FormatInt(source, 10) + "/merge-into/" + strconv.FormatInt(target, 10)
	w := call(t, mux, http.MethodPost, path, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	want := productOf(t, db, target)
	if got := ingredientProductID(t, db, recipeID); !got.Valid || got.Int64 != want {
		t.Errorf("expected recipe to relink to merge target product %d, got %+v", want, got)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM products`); n != 1 {
		t.Errorf("emptied source product should be folded away, %d products", n)
	}
}

func TestPatchTargetsOnlyThatLot(t *testing.T) {
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	a := insertLot(t, db, "milk", 5, "L", "2026-05-01", "")
	b := insertLot(t, db, "milk", 3, "L", "2026-06-01", "")
	w := call(t, mux, http.MethodPatch, "/api/inventory/"+strconv.FormatInt(b, 10), `{"quantity":1}`)
	if w.Code != 200 {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	var qa, qb float64
	db.QueryRow(`SELECT quantity FROM stock_lots WHERE id=?`, a).Scan(&qa)
	db.QueryRow(`SELECT quantity FROM stock_lots WHERE id=?`, b).Scan(&qb)
	if qa != 5 || qb != 1 {
		t.Fatalf("a=%v b=%v", qa, qb)
	}
	var ct string
	var before, after float64
	if err := db.QueryRow(`SELECT change_type, quantity_before, quantity_after FROM inventory_history WHERE inventory_id=?`, b).Scan(&ct, &before, &after); err != nil {
		t.Fatal(err)
	}
	if ct != "deduct" || before != 3 || after != 1 {
		t.Fatalf("history %s %v %v", ct, before, after)
	}
}

func TestPatchMixedFieldsAllApplied(t *testing.T) {
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	id := insertLot(t, db, "rice", 5, "kg", "", "")
	w := call(t, mux, http.MethodPatch, "/api/inventory/"+strconv.FormatInt(id, 10),
		`{"quantity":1,"location":"freezer","expiration_date":"2026-09-09","barcode":"X1","quantity_per_scan":2,"unit_cost_cents":150}`)
	if w.Code != 200 {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	var q, qps float64
	var loc, exp, bc string
	var cost int64
	db.QueryRow(`SELECT l.quantity,l.location,l.expiration_date,p.barcode,p.quantity_per_scan,l.unit_cost_cents FROM stock_lots l JOIN products p ON p.id=l.product_id WHERE l.id=?`, id).Scan(&q, &loc, &exp, &bc, &qps, &cost)
	if q != 1 || loc != "freezer" || exp != "2026-09-09" || bc != "X1" || qps != 2 || cost != 150 {
		t.Fatalf("got %v %s %s %s %v %d", q, loc, exp, bc, qps, cost)
	}
}

func TestPatchRejectsWrongTypes(t *testing.T) {
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	id := insertLot(t, db, "rice", 5, "kg", "", "")
	u := "/api/inventory/" + strconv.FormatInt(id, 10)
	for _, body := range []string{`{"quantity":"5"}`, `{"unit_cost_cents":"abc"}`, `{"low_threshold":"1"}`, `{"location":"x","quantity":"5"}`} {
		if w := call(t, mux, http.MethodPatch, u, body); w.Code != 400 {
			t.Errorf("%s: got %d", body, w.Code)
		}
	}
	var typ string
	var q float64
	var loc string
	db.QueryRow(`SELECT typeof(quantity), quantity, location FROM stock_lots WHERE id=?`, id).Scan(&typ, &q, &loc)
	if typ != "real" || q != 5 || loc == "x" {
		t.Fatalf("changed: %s %v %s", typ, q, loc)
	}
	if w := call(t, mux, http.MethodPatch, "/api/inventory/99999", `{"quantity":1}`); w.Code != 404 {
		t.Errorf("missing: %d", w.Code)
	}
}
