package handlers_test

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
)

func TestDBErrorReturnsGeneric500(t *testing.T) {
	mux, db := newMux(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	db.Close()

	w := call(t, mux, "GET", "/api/inventory/", "")
	if w.Code != 500 {
		t.Fatalf("code = %d", w.Code)
	}
	var out map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["error"] != "internal server error" {
		t.Errorf("error = %q", out["error"])
	}
	b := strings.ToLower(w.Body.String())
	if strings.Contains(b, "sql") || strings.Contains(b, "closed") {
		t.Errorf("body leaks details: %s", b)
	}
	if !strings.Contains(buf.String(), "closed") {
		t.Errorf("real error not logged: %q", buf.String())
	}
}

func TestLogHistoryFailureFailsRequest(t *testing.T) {
	mux, db := newMux(t)
	if _, err := db.Exec("DROP TABLE inventory_history"); err != nil {
		t.Fatal(err)
	}
	w := call(t, mux, "POST", "/api/inventory/", `{"name":"Flour","quantity":1,"unit":"kg"}`)
	if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	var n int
	db.QueryRow("SELECT COUNT(*) FROM inventory").Scan(&n)
	if n != 0 {
		t.Errorf("inventory rows = %d, want 0", n)
	}
}
