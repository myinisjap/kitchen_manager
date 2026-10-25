package handlers_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestAddToShoppingListIncompatibleUnits(t *testing.T) {
	mux, db := newMux(t)
	inv := insertLot(t, db, "Milk", 1000, "g", "", "")
	rid := newRecipe(t, db, "pudding", 1)
	if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id,inventory_id,name,quantity,unit) VALUES (?,?,'Milk',2,'cup')`, rid, inv); err != nil {
		t.Fatal(err)
	}
	w := call(t, mux, "POST", fmt.Sprintf("/api/recipes/%d/add-to-shopping-list", rid), `{}`)
	if w.Code != 200 {
		t.Fatalf("code %d: %s", w.Code, w.Body.String())
	}
	var unit string
	var qty float64
	var invID int64
	if err := db.QueryRow(`SELECT unit, quantity_needed, inventory_id FROM shopping_list`).Scan(&unit, &qty, &invID); err != nil {
		t.Fatal(err)
	}
	if unit != "cup" || qty != 2 || invID != inv {
		t.Errorf("got %v %v %v", unit, qty, invID)
	}
}

func TestAvailabilityIncompatibleUnits(t *testing.T) {
	mux, db := newMux(t)
	inv := insertLot(t, db, "Milk", 1000, "g", "", "")
	bad := newRecipe(t, db, "pudding", 1)
	good := newRecipe(t, db, "porridge", 1)
	db.Exec(`INSERT INTO recipe_ingredients (recipe_id,inventory_id,name,quantity,unit) VALUES (?,?,'Milk',2,'cup')`, bad, inv)
	db.Exec(`INSERT INTO recipe_ingredients (recipe_id,inventory_id,name,quantity,unit) VALUES (?,?,'Milk',100,'g')`, good, inv)

	list := func(path string) map[string][]string {
		w := call(t, mux, "GET", path, "")
		if w.Code != 200 {
			t.Fatalf("code %d", w.Code)
		}
		var rs []struct {
			Name   string   `json:"name"`
			Incomp []string `json:"incompatible_ingredients"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &rs); err != nil {
			t.Fatal(err)
		}
		m := map[string][]string{}
		for _, r := range rs {
			m[r.Name] = r.Incomp
		}
		return m
	}
	only := list("/api/recipes/?available_only=true")
	if _, ok := only["pudding"]; ok {
		t.Errorf("incompatible recipe listed as available")
	}
	if _, ok := only["porridge"]; !ok {
		t.Errorf("compatible recipe missing")
	}
	all := list("/api/recipes/")
	if len(all["pudding"]) != 1 || all["pudding"][0] != "Milk" {
		t.Errorf("pudding incompatible: %v", all["pudding"])
	}
	if len(all["porridge"]) != 0 {
		t.Errorf("porridge incompatible: %v", all["porridge"])
	}
}
