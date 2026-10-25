package handlers_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/testutil"
)

var baseLotKeys = []string{"barcode", "expiration_date", "id", "location", "low_threshold", "name",
	"preferred_unit", "product_id", "quantity", "quantity_per_scan", "unit", "unit_cost_cents"}

func keysOf(t *testing.T, body []byte) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func wantKeys(t *testing.T, label string, body []byte, extra ...string) {
	t.Helper()
	want := append(append([]string{}, baseLotKeys...), extra...)
	sort.Strings(want)
	got := keysOf(t, body)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s keys:\n got %v\nwant %v", label, got, want)
	}
}

func TestLotShapes(t *testing.T) {
	mux, db := newMux(t)
	handlers.RegisterProducts(mux, db)
	_, lot := testutil.AddLot(t, db, "Flour", 5, "kg", testutil.WithBarcode("111"), testutil.WithQtyPerScan(1))
	pid, _ := testutil.AddLot(t, db, "Rice", 2, "kg")
	_ = pid

	w := call(t, mux, "GET", fmt.Sprintf("/api/inventory/%d", lot), "")
	wantKeys(t, "get", w.Body.Bytes())
	// alphabetical order of the raw bytes
	re := regexp.MustCompile(`"([a-z_]+)":`)
	var order []string
	for _, m := range re.FindAllStringSubmatch(w.Body.String(), -1) {
		order = append(order, m[1])
	}
	if !sort.StringsAreSorted(order) {
		t.Errorf("keys not alphabetical: %v", order)
	}

	w = call(t, mux, "GET", "/api/inventory/barcode/111", "")
	wantKeys(t, "barcode primary", w.Body.Bytes())

	w = call(t, mux, "POST", fmt.Sprintf("/api/skus/item/%d", lot), `{"barcode":"222","quantity_per_scan":2}`)
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("sku post %d %s", w.Code, w.Body.String())
	}
	var sk map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &sk)
	wantKeys(t, "sku post item", sk["item"])
	w = call(t, mux, "GET", "/api/inventory/barcode/222", "")
	wantKeys(t, "barcode alias", w.Body.Bytes(), "sku_id", "matched_barcode")

	w = call(t, mux, "GET", "/api/products/"+fmt.Sprint(productOf(t, db, lot)), "")
	var pv struct {
		Lots []map[string]any `json:"lots"`
	}
	json.Unmarshal(w.Body.Bytes(), &pv)
	if len(pv.Lots) != 1 {
		t.Fatalf("lots: %s", w.Body.String())
	}
	if v, ok := pv.Lots[0]["unit_mismatch"]; !ok || v != false {
		t.Errorf("unit_mismatch: %v %v", v, ok)
	}

	w = call(t, mux, "POST", fmt.Sprintf("/api/inventory/%d/deduct", lot), `{"quantity":1}`)
	wantKeys(t, "deduct", w.Body.Bytes(), "deducted")

	w = call(t, mux, "GET", "/api/inventory/suggestions?q=Fl", "")
	var sug []map[string]any
	json.Unmarshal(w.Body.Bytes(), &sug)
	if len(sug) == 0 {
		t.Fatalf("no suggestions: %s", w.Body.String())
	}
	ks := []string{}
	for k := range sug[0] {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	if strings.Join(ks, ",") != "location,low_threshold,name,preferred_unit,unit" {
		t.Errorf("suggestion keys %v", ks)
	}

	w = call(t, mux, "GET", "/api/inventory/similar?name=Flour", "")
	var sim []map[string]any
	json.Unmarshal(w.Body.Bytes(), &sim)
	if len(sim) == 0 {
		t.Fatalf("no similar: %s", w.Body.String())
	}
	ks = ks[:0]
	for k := range sim[0] {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	if strings.Join(ks, ",") != "id,location,name,preferred_unit,product_id,quantity,unit" {
		t.Errorf("similar keys %v", ks)
	}

	// deduct to empty -> stub with lot_id
	w = call(t, mux, "POST", fmt.Sprintf("/api/inventory/%d/deduct", lot), `{"quantity":4}`)
	wantKeys(t, "deduct empty", w.Body.Bytes(), "deducted", "lot_id")
}
