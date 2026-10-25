package handlers_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const nConc = 50

// fire runs n concurrent requests and returns their status codes.
func fire(mux *http.ServeMux, n int, method string, path func(i int) string, body func(i int) string) []int {
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(method, path(i), bytes.NewBufferString(body(i)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	return codes
}

func checkCodes(t *testing.T, codes []int) {
	t.Helper()
	for i, c := range codes {
		if c != http.StatusOK && c != http.StatusCreated {
			t.Errorf("request %d: status %d", i, c)
		}
	}
}

func TestConcurrentInventoryPostMergesAtomically(t *testing.T) {
	mux, db := newSchemaMux(t)
	const body = `{"name":"milk","quantity":1,"unit":"piece","barcode":"123"}`
	if w := call(t, mux, "POST", "/api/inventory/", body); w.Code != http.StatusCreated {
		t.Fatalf("seed: %d %s", w.Code, w.Body.String())
	}
	codes := fire(mux, nConc, "POST", func(int) string { return "/api/inventory/" }, func(int) string { return body })
	checkCodes(t, codes)
	var n int
	var qty float64
	if err := db.QueryRow(`SELECT COUNT(*), SUM(quantity) FROM inventory WHERE barcode='123'`).Scan(&n, &qty); err != nil {
		t.Fatal(err)
	}
	if n != 1 || qty != nConc+1 {
		t.Fatalf("rows=%d qty=%v, want 1 row qty %d", n, qty, nConc+1)
	}
	var h int
	db.QueryRow(`SELECT COUNT(*) FROM inventory_history`).Scan(&h)
	if h != nConc+1 {
		t.Errorf("history rows = %d, want %d", h, nConc+1)
	}
}

func TestConcurrentInventoryPostFromEmpty(t *testing.T) {
	mux, db := newSchemaMux(t)
	const body = `{"name":"eggs","quantity":1,"unit":"piece","barcode":"555"}`
	checkCodes(t, fire(mux, nConc, "POST", func(int) string { return "/api/inventory/" }, func(int) string { return body }))
	var n int
	var qty float64
	if err := db.QueryRow(`SELECT COUNT(*), SUM(quantity) FROM inventory WHERE barcode='555'`).Scan(&n, &qty); err != nil {
		t.Fatal(err)
	}
	if n != 1 || qty != nConc {
		t.Fatalf("rows=%d qty=%v, want 1 row qty %d", n, qty, nConc)
	}
}

func TestConcurrentSkuAliasPostMergesAtomically(t *testing.T) {
	mux, db := newSchemaMux(t)
	id := insertLot(t, db, "flour", 5, "piece", "", "")
	if w := call(t, mux, "POST", fmt.Sprintf("/api/skus/item/%d", id), `{"barcode":"999","quantity":0}`); w.Code != http.StatusCreated {
		t.Fatalf("alias: %d %s", w.Code, w.Body.String())
	}
	body := `{"name":"other name","quantity":1,"unit":"piece","barcode":"999"}`
	checkCodes(t, fire(mux, nConc, "POST", func(int) string { return "/api/inventory/" }, func(int) string { return body }))
	var qty float64
	db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, id).Scan(&qty)
	if qty != 5+nConc {
		t.Fatalf("qty=%v want %d", qty, 5+nConc)
	}
}

func TestConcurrentMergeInto(t *testing.T) {
	mux, db := newSchemaMux(t)
	const n = 20
	target := insertLot(t, db, "rice", 10, "piece", "", "")
	srcs := make([]int64, n)
	for i := range srcs {
		srcs[i] = insertLot(t, db, fmt.Sprintf("rice%d", i), 1, "piece", "", "")
	}
	checkCodes(t, fire(mux, n, "POST",
		func(i int) string { return fmt.Sprintf("/api/inventory/%d/merge-into/%d", srcs[i], target) },
		func(int) string { return `{}` }))
	var qty float64
	db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, target).Scan(&qty)
	var rows int
	db.QueryRow(`SELECT COUNT(*) FROM inventory`).Scan(&rows)
	if qty != 10+n || rows != 1 {
		t.Fatalf("qty=%v rows=%d, want %d and 1", qty, rows, 10+n)
	}
}
