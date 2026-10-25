package handlers_test

import (
	"fmt"
	"testing"

	"kitchen_manager/services"
)

func TestCreateKeepsNameCase(t *testing.T) {
	mux, db := newMux(t)
	id := createLot(t, mux, `{"name":" Milk ","quantity":1,"unit":"L","location":"fridge"}`)
	var name string
	if err := db.QueryRow(`SELECT name FROM inventory WHERE id=?`, int64(id)).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Milk" {
		t.Fatalf("got %q", name)
	}
}

func TestMergeIsCaseInsensitive(t *testing.T) {
	mux, db := newMux(t)
	f := `{"name":"%s","quantity":1,"unit":"L","location":"fridge","barcode":"X1","expiration_date":"2030-01-01"}`
	a := createLot(t, mux, fmt.Sprintf(f, "Milk"))
	b := createLot(t, mux, fmt.Sprintf(f, "milk"))
	if a != b {
		t.Fatalf("ids differ %v %v", a, b)
	}
	var n int
	var name string
	var q float64
	db.QueryRow(`SELECT COUNT(*), MIN(name), SUM(quantity) FROM inventory`).Scan(&n, &name, &q)
	if n != 1 || name != "Milk" || q != 2 {
		t.Fatal(n, name, q)
	}
}

func TestParSharedAcrossCase(t *testing.T) {
	mux, db := newMux(t)
	a := createLot(t, mux, `{"name":"Milk","quantity":1,"unit":"L","location":"fridge"}`)
	createLot(t, mux, `{"name":"milk","quantity":1,"unit":"L","location":"garage"}`)
	call(t, mux, "PATCH", fmt.Sprintf("/api/inventory/%v", a), `{"low_threshold":5}`)
	levels, err := services.ProductLevels(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(levels) != 1 || levels[0].Par != 5 || levels[0].Total != 2 || len(levels[0].LotIDs) != 2 {
		t.Fatalf("%+v", levels)
	}
	if n, err := services.GenerateFromThresholds(db); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var c int
	db.QueryRow(`SELECT COUNT(*) FROM shopping_list`).Scan(&c)
	if c != 1 {
		t.Fatal(c)
	}
}
