package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func parOf(t *testing.T, mux *http.ServeMux, id any) float64 {
	t.Helper()
	w := call(t, mux, "GET", fmt.Sprintf("/api/inventory/%v", id), "")
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m["low_threshold"].(float64)
}

func createLot(t *testing.T, mux *http.ServeMux, body string) float64 {
	t.Helper()
	w := call(t, mux, "POST", "/api/inventory/", body)
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	return m["id"].(float64)
}

func TestParLevel_DefaultZeroAndInherit(t *testing.T) {
	mux, _ := newMux(t)
	a := createLot(t, mux, `{"name":"milk","quantity":1,"unit":"L","location":"fridge"}`)
	if p := parOf(t, mux, a); p != 0 {
		t.Fatalf("default par %v", p)
	}
	call(t, mux, "PATCH", fmt.Sprintf("/api/inventory/%v", a), `{"low_threshold":4}`)
	b := createLot(t, mux, `{"name":"milk","quantity":1,"unit":"L","location":"garage"}`)
	if p := parOf(t, mux, b); p != 4 {
		t.Fatalf("inherit par %v", p)
	}
}

func TestParLevel_PostAndPatchUpdateAllLots(t *testing.T) {
	mux, _ := newMux(t)
	a := createLot(t, mux, `{"name":"milk","quantity":1,"unit":"L","location":"fridge"}`)
	b := createLot(t, mux, `{"name":"milk","quantity":1,"unit":"L","location":"garage","low_threshold":5}`)
	if parOf(t, mux, a) != 5 || parOf(t, mux, b) != 5 {
		t.Fatal("post did not propagate")
	}
	call(t, mux, "PATCH", fmt.Sprintf("/api/inventory/%v", a), `{"low_threshold":3}`)
	if parOf(t, mux, b) != 3 {
		t.Fatal("patch did not propagate")
	}
	call(t, mux, "PATCH", fmt.Sprintf("/api/inventory/%v", b), `{"low_threshold":0}`)
	if parOf(t, mux, a) != 0 {
		t.Fatal("clear did not propagate")
	}
}

func TestParLevel_PatchDeductionKeepsPar(t *testing.T) {
	mux, _ := newMux(t)
	a := createLot(t, mux, `{"name":"milk","quantity":5,"unit":"L","location":"fridge"}`)
	w := call(t, mux, "PATCH", fmt.Sprintf("/api/inventory/%v", a), `{"quantity":2,"low_threshold":6}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if parOf(t, mux, a) != 6 {
		t.Fatal("par lost on deduction")
	}
}

func TestParLevel_Grouped(t *testing.T) {
	mux, _ := newMux(t)
	createLot(t, mux, `{"name":"milk","quantity":1,"unit":"L","location":"fridge","low_threshold":3}`)
	createLot(t, mux, `{"name":"milk","quantity":1,"unit":"L","location":"garage"}`)
	get := func() map[string]any {
		w := call(t, mux, "GET", "/api/inventory/grouped?location=fridge", "")
		var out []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out) != 1 {
			t.Fatalf("%v %s", err, w.Body.String())
		}
		return out[0]
	}
	g := get()
	if g["par_level"].(float64) != 3 || g["product_total"].(float64) != 2 || g["below_par"] != true {
		t.Fatalf("%v", g)
	}
	createLot(t, mux, `{"name":"milk","quantity":5,"unit":"L","location":"cellar"}`)
	if g = get(); g["below_par"] != false || g["product_total"].(float64) != 7 {
		t.Fatalf("%v", g)
	}
}
