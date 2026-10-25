package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"kitchen_manager/internal/testutil"
)

func groupedList(t *testing.T, mux *http.ServeMux, q string) []map[string]any {
	t.Helper()
	w := call(t, mux, "GET", "/api/inventory/grouped"+q, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGroupedIncludesOutOfStockProducts(t *testing.T) {
	mux, db := newAllMux(t)
	testutil.AddLot(t, db, "Apple", 3, "piece", testutil.WithLocation("Pantry"))
	_, b := testutil.AddLot(t, db, "Butter", 100, "g", testutil.WithPar(50), testutil.WithBarcode("111"))
	testutil.AddLot(t, db, "Cheese", 5, "piece", testutil.WithLocation("Pantry"))
	w := call(t, mux, "POST", fmt.Sprintf("/api/inventory/%d/deduct", b), `{"quantity":100}`)
	m := decodeMap(t, w.Body.Bytes())
	if w.Code != 200 || m["id"].(float64) != 0 || int64(m["lot_id"].(float64)) != b || m["deducted"].(float64) != 100 {
		t.Fatalf("%d %v", w.Code, m)
	}
	g := groupedList(t, mux, "")
	if len(g) != 3 || g[1]["name"] != "Butter" {
		t.Fatalf("order: %v", g)
	}
	bu := g[1]
	if bu["out_of_stock"] != true || len(bu["locations"].([]any)) != 0 || bu["total_quantity"].(float64) != 0 ||
		bu["recommended_location_id"].(float64) != 0 || bu["below_par"] != true || bu["barcode"] != "111" {
		t.Fatalf("%v", bu)
	}
	if g[0]["out_of_stock"] != false {
		t.Fatal(g[0])
	}
	if g := groupedList(t, mux, "?location=Pantry"); len(g) != 2 {
		t.Fatalf("location filter: %v", g)
	}
	if g := groupedList(t, mux, "?name=butt"); len(g) != 1 {
		t.Fatalf("name filter: %v", g)
	}
	if g := groupedList(t, mux, "?name=app"); len(g) != 1 || g[0]["name"] != "Apple" {
		t.Fatalf("name filter: %v", g)
	}
}

func TestPatchLastLotToZeroReturnsStub(t *testing.T) {
	mux, db := newAllMux(t)
	pid, a := testutil.AddLot(t, db, "Milk", 2, "L")
	w := call(t, mux, "PATCH", fmt.Sprintf("/api/inventory/%d", a), `{"quantity":0}`)
	m := decodeMap(t, w.Body.Bytes())
	if w.Code != 200 || m["id"].(float64) != 0 || int64(m["product_id"].(float64)) != pid || int64(m["lot_id"].(float64)) != a {
		t.Fatalf("%d %v", w.Code, m)
	}
}

func TestGetProduct(t *testing.T) {
	mux, db := newAllMux(t)
	pid, _ := testutil.AddLot(t, db, "Egg", 6, "piece", testutil.WithPar(10))
	testutil.AddLot(t, db, "Egg", 100, "g", testutil.WithLocation("Fridge"))
	db.Exec(`INSERT INTO product_skus (product_id,barcode,quantity_per_scan) VALUES (?,?,?)`, pid, "999", 12)
	w := call(t, mux, "GET", fmt.Sprintf("/api/products/%d", pid), "")
	m := decodeMap(t, w.Body.Bytes())
	if w.Code != 200 || m["total_quantity"].(float64) != 6 || m["below_par"] != true || m["out_of_stock"] != false {
		t.Fatalf("%d %v", w.Code, m)
	}
	mism := 0
	for _, l := range m["lots"].([]any) {
		if l.(map[string]any)["unit_mismatch"] == true {
			mism++
		}
	}
	if len(m["lots"].([]any)) != 2 || mism != 1 || len(m["skus"].([]any)) != 1 {
		t.Fatalf("%v", m)
	}
	if w := call(t, mux, "GET", "/api/products/9999", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := call(t, mux, "GET", "/api/products/abc", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestPatchProduct(t *testing.T) {
	mux, db := newAllMux(t)
	pid, _ := testutil.AddLot(t, db, "Rice", 500, "g")
	testutil.AddLot(t, db, "Beans", 5, "can", testutil.WithBarcode("777"))
	url := fmt.Sprintf("/api/products/%d", pid)
	w := call(t, mux, "PATCH", url, `{"par_level":2,"preferred_unit":"kg","quantity_per_scan":3,"barcode":"555"}`)
	m := decodeMap(t, w.Body.Bytes())
	if w.Code != 200 || m["par_level"].(float64) != 2 || m["preferred_unit"] != "kg" || m["quantity_per_scan"].(float64) != 3 || m["barcode"] != "555" {
		t.Fatalf("%d %v", w.Code, m)
	}
	if w := call(t, mux, "PATCH", url, `{"name":"RICE"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(t, mux, "PATCH", url, `{"name":"beans"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call(t, mux, "PATCH", url, `{"barcode":"777"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call(t, mux, "PATCH", url, `{"par_level":"x"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call(t, mux, "PATCH", url, `{"preferred_unit":"L"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call(t, mux, "PATCH", url, `{}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call(t, mux, "PATCH", "/api/products/9999", `{"par_level":1}`); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestMergeProductInto(t *testing.T) {
	mux, db := newAllMux(t)
	src, a := testutil.AddLot(t, db, "Tomatoes", 4, "piece", testutil.WithBarcode("123"))
	dst, _ := testutil.AddLot(t, db, "Tomato", 2, "piece")
	rid := recipeWith(t, db, a, "Tomatoes", 1, "piece")
	db.Exec(`INSERT INTO shopping_list (name,product_id,quantity_needed,unit) VALUES ('Tomatoes',?,1,'piece')`, src)
	url := func(a, b int64) string { return fmt.Sprintf("/api/products/%d/merge-into/%d", a, b) }
	if w := call(t, mux, "POST", url(src, src), `{}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call(t, mux, "POST", url(9999, dst), `{}`); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := call(t, mux, "POST", url(src, 9999), `{}`); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w := call(t, mux, "POST", url(src, dst), `{}`)
	m := decodeMap(t, w.Body.Bytes())
	if w.Code != 200 || len(m["lots"].([]any)) != 2 || m["total_quantity"].(float64) != 6 || m["barcode"] != "123" {
		t.Fatalf("%d %v", w.Code, m)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM products WHERE id=?`, src) != 0 ||
		countRows(t, db, `SELECT COUNT(*) FROM recipe_ingredients WHERE recipe_id=? AND product_id=?`, rid, dst) != 1 ||
		countRows(t, db, `SELECT COUNT(*) FROM shopping_list WHERE product_id=?`, dst) != 1 {
		t.Fatal("merge did not relink")
	}
}

func TestDeleteProduct(t *testing.T) {
	mux, db := newAllMux(t)
	pid, a := testutil.AddLot(t, db, "Jam", 1, "jar")
	testutil.AddLot(t, db, "Jam", 2, "jar", testutil.WithLocation("Cellar"))
	db.Exec(`INSERT INTO product_skus (product_id,barcode,quantity_per_scan) VALUES (?,?,1)`, pid, "42")
	rid := recipeWith(t, db, a, "Jam", 1, "jar")
	url := fmt.Sprintf("/api/products/%d", pid)
	if w := call(t, mux, "DELETE", url, ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if countRows(t, db, `SELECT COUNT(*) FROM stock_lots`) != 0 || countRows(t, db, `SELECT COUNT(*) FROM product_skus`) != 0 ||
		countRows(t, db, `SELECT COUNT(*) FROM recipe_ingredients WHERE recipe_id=? AND product_id IS NULL`, rid) != 1 ||
		countRows(t, db, `SELECT COUNT(*) FROM inventory_history WHERE change_type='delete' AND product_id=?`, pid) != 2 {
		t.Fatal("delete cleanup wrong")
	}
	if w := call(t, mux, "DELETE", url, ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestHistoryByProduct(t *testing.T) {
	mux, db := newAllMux(t)
	pid, a := testutil.AddLot(t, db, "Oats", 100, "g")
	_, b := testutil.AddLot(t, db, "Oats", 100, "g", testutil.WithLocation("Cellar"))
	_, c := testutil.AddLot(t, db, "Rye", 100, "g")
	for _, id := range []int64{a, b, c} {
		call(t, mux, "PATCH", fmt.Sprintf("/api/inventory/%d", id), `{"quantity":50}`)
	}
	get := func(url string) []map[string]any {
		w := call(t, mux, "GET", url, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var rows []map[string]any
		json.Unmarshal(w.Body.Bytes(), &rows)
		return rows
	}
	url := fmt.Sprintf("/api/history/product/%d", pid)
	rows := get(url)
	if len(rows) != 2 {
		t.Fatalf("%v", rows)
	}
	for _, r := range rows {
		if int64(r["product_id"].(float64)) != pid {
			t.Fatal(r)
		}
	}
	call(t, mux, "DELETE", fmt.Sprintf("/api/products/%d", pid), "")
	if rows := get(url); len(rows) != 4 {
		t.Fatalf("history should outlive product: %v", rows)
	}
	if w := call(t, mux, "GET", "/api/history/product/x", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := call(t, mux, "GET", "/api/history", "")
	m := decodeMap(t, w.Body.Bytes())
	r0 := m["rows"].([]any)[0].(map[string]any)
	if _, ok := r0["product_id"]; !ok {
		t.Fatal(r0)
	}
}
