package handlers_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"kitchen_manager/internal/auth"
)

func changedBy(t *testing.T, trust bool) string {
	mux, db := newMux(t)
	var h http.Handler = mux
	if trust {
		h = auth.ForwardedUserMiddleware(mux)
	}
	body := `{"name":"Milk","quantity":1,"unit":"L","location":"fridge"}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	req.Header.Set("X-Forwarded-User", "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var got string
	if err := db.QueryRow(`SELECT COALESCE(changed_by,'') FROM inventory_history WHERE item_name='milk'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestChangedByFromForwardedUser(t *testing.T) {
	if got := changedBy(t, true); got != "alice@example.com" {
		t.Errorf("changed_by = %q", got)
	}
}

func TestChangedByEmptyWithoutMiddleware(t *testing.T) {
	if got := changedBy(t, false); got != "" {
		t.Errorf("changed_by = %q", got)
	}
}
