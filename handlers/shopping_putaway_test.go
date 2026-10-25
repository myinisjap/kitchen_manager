package handlers_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func insertShopping(t *testing.T, db *sql.DB, inv *int64, name string, qty float64, unit string, checked bool) int64 {
	t.Helper()
	var id int64
	c := 0
	if checked {
		c = 1
	}
	if err := db.QueryRow(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,?,'manual') RETURNING id`,
		inv, name, qty, unit, c).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func countRows(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func bought(t *testing.T, mux *http.ServeMux, id int64, body string) (int, map[string]any) {
	w := call(t, mux, "POST", fmt.Sprintf("/api/shopping/%d/bought", id), body)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func TestBoughtLinkedSameUnit(t *testing.T) {
	mux, db := newSchemaMux(t)
	lot := insertLot(t, db, "milk", 1, "L", "", "")
	sid := insertShopping(t, db, &lot, "milk", 2, "L", false)
	code, _ := bought(t, mux, sid, "")
	if code != 200 {
		t.Fatal(code)
	}
	if q := qtyOf(t, db, lot); q != 3 {
		t.Fatalf("qty %v", q)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM shopping_list WHERE id=?`, sid) != 0 {
		t.Fatal("row kept")
	}
	var b, a float64
	if err := db.QueryRow(`SELECT quantity_before,quantity_after FROM inventory_history WHERE source='shopping_bought'`).Scan(&b, &a); err != nil || b != 1 || a != 3 {
		t.Fatal(err, b, a)
	}
}

func TestBoughtNewProduct(t *testing.T) {
	mux, db := newSchemaMux(t)
	sid := insertShopping(t, db, nil, "Bananas", 6, "piece", false)
	code, m := bought(t, mux, sid, `{"location":"counter","expiration_date":"2030-01-05"}`)
	if code != 200 || m["created"] != true {
		t.Fatal(code, m)
	}
	var name, unit, loc, exp string
	var q float64
	if err := db.QueryRow(`SELECT name,quantity,unit,location,expiration_date FROM inventory`).Scan(&name, &q, &unit, &loc, &exp); err != nil {
		t.Fatal(err)
	}
	if name != "bananas" || q != 6 || unit != "piece" || loc != "counter" || exp != "2030-01-05" {
		t.Fatal(name, q, unit, loc, exp)
	}
}

func TestBoughtNameMatch(t *testing.T) {
	mux, db := newSchemaMux(t)
	lot := insertLot(t, db, "eggs", 2, "piece", "", "")
	sid := insertShopping(t, db, nil, "Eggs", 6, "piece", false)
	if code, _ := bought(t, mux, sid, `{}`); code != 200 {
		t.Fatal(code)
	}
	if qtyOf(t, db, lot) != 8 || countRows(t, db, `SELECT COUNT(*) FROM inventory`) != 1 {
		t.Fatal("not merged")
	}
}

func TestBoughtConversion(t *testing.T) {
	mux, db := newSchemaMux(t)
	lot := insertLot(t, db, "flour", 1, "g", "", "")
	db.Exec(`UPDATE inventory SET preferred_unit='kg' WHERE id=?`, lot)
	sid := insertShopping(t, db, &lot, "flour", 500, "g", false)
	if code, _ := bought(t, mux, sid, ``); code != 200 {
		t.Fatal(code)
	}
	if q := qtyOf(t, db, lot); q != 1.5 {
		t.Fatal(q)
	}
	lot2 := insertLot(t, db, "rice", 500, "g", "", "")
	sid2 := insertShopping(t, db, &lot2, "rice", 1, "kg", false)
	if code, _ := bought(t, mux, sid2, ``); code != 200 {
		t.Fatal(code)
	}
	if q := qtyOf(t, db, lot2); q != 1500 {
		t.Fatal(q)
	}
}

func TestBoughtIncompatible(t *testing.T) {
	mux, db := newSchemaMux(t)
	lot := insertLot(t, db, "sugar", 100, "g", "", "")
	sid := insertShopping(t, db, &lot, "sugar", 2, "cup", false)
	if code, _ := bought(t, mux, sid, ``); code != 400 {
		t.Fatal(code)
	}
	if qtyOf(t, db, lot) != 100 || countRows(t, db, `SELECT COUNT(*) FROM shopping_list`) != 1 {
		t.Fatal("changed")
	}
}

func TestBoughtDifferentExpiryNewLot(t *testing.T) {
	mux, db := newSchemaMux(t)
	lot := insertLot(t, db, "yogurt", 1, "piece", "2030-01-01", "")
	db.Exec(`UPDATE inventory SET preferred_unit='piece', low_threshold=4 WHERE id=?`, lot)
	sid := insertShopping(t, db, &lot, "yogurt", 3, "piece", false)
	code, m := bought(t, mux, sid, `{"expiration_date":"2030-02-02"}`)
	if code != 200 || m["created"] != true {
		t.Fatal(code, m)
	}
	if qtyOf(t, db, lot) != 1 {
		t.Fatal("old lot changed")
	}
	var th float64
	var pref string
	if err := db.QueryRow(`SELECT low_threshold,preferred_unit FROM inventory WHERE id<>?`, lot).Scan(&th, &pref); err != nil || th != 4 || pref != "piece" {
		t.Fatal(err, th, pref)
	}
}

func TestBoughtZeroLotReused(t *testing.T) {
	mux, db := newSchemaMux(t)
	lot := insertLot(t, db, "tea", 0, "piece", "", "")
	sid := insertShopping(t, db, &lot, "tea", 5, "piece", false)
	if code, _ := bought(t, mux, sid, `{"expiration_date":"2031-03-03"}`); code != 200 {
		t.Fatal(code)
	}
	var exp string
	db.QueryRow(`SELECT expiration_date FROM inventory WHERE id=?`, lot).Scan(&exp)
	if qtyOf(t, db, lot) != 5 || exp != "2031-03-03" || countRows(t, db, `SELECT COUNT(*) FROM inventory`) != 1 {
		t.Fatal("not reused")
	}
}

func TestBought404AndDouble(t *testing.T) {
	mux, db := newSchemaMux(t)
	if code, _ := bought(t, mux, 999, ``); code != 404 {
		t.Fatal(code)
	}
	lot := insertLot(t, db, "salt", 1, "g", "", "")
	sid := insertShopping(t, db, &lot, "salt", 1, "g", false)
	bought(t, mux, sid, ``)
	if code, _ := bought(t, mux, sid, ``); code != 404 {
		t.Fatal(code)
	}
	if qtyOf(t, db, lot) != 2 {
		t.Fatal("double add")
	}
}

func TestPutAwayBatch(t *testing.T) {
	mux, db := newSchemaMux(t)
	lot := insertLot(t, db, "milk", 1, "L", "", "")
	a := insertShopping(t, db, &lot, "milk", 2, "L", true)
	b := insertShopping(t, db, nil, "Apples", 3, "piece", true)
	u := insertShopping(t, db, nil, "Pears", 3, "piece", false)
	sugar := insertLot(t, db, "sugar", 100, "g", "", "")
	bad := insertShopping(t, db, &sugar, "sugar", 2, "cup", true)
	w := call(t, mux, "POST", "/api/shopping/put-away", fmt.Sprintf(`{"items":[{"id":%d,"location":"shelf"},{"id":%d,"quantity":4}]}`, b, a))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var m struct {
		PutAway []map[string]any `json:"put_away"`
		Skipped []map[string]any `json:"skipped"`
	}
	json.Unmarshal(w.Body.Bytes(), &m)
	if len(m.PutAway) != 2 || len(m.Skipped) != 1 {
		t.Fatal(w.Body.String())
	}
	if qtyOf(t, db, lot) != 5 {
		t.Fatal("override ignored")
	}
	if countRows(t, db, `SELECT COUNT(*) FROM inventory WHERE name='apples' AND location='shelf' AND quantity=3`) != 1 {
		t.Fatal("apples")
	}
	for _, id := range []int64{u, bad} {
		if countRows(t, db, `SELECT COUNT(*) FROM shopping_list WHERE id=?`, id) != 1 {
			t.Fatal("row removed", id)
		}
	}
	if countRows(t, db, `SELECT COUNT(*) FROM shopping_list`) != 2 {
		t.Fatal("wrong remaining")
	}
}

func TestShoppingCreateStillWorks(t *testing.T) {
	mux, _ := newSchemaMux(t)
	w := call(t, mux, "POST", "/api/shopping/", `{"name":"x"}`)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code)
	}
}
