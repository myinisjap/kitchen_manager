package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A fresh database is seeded with default_servings by the schema migration, so
// "unconfigured" means exactly those defaults and nothing else.
func TestGetSettingsDefaults(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("GET", "/api/settings", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 1 || resp["default_servings"] != "2" {
		t.Errorf("want only seeded default_servings=2, got %v", resp)
	}
}

func TestPatchSettings(t *testing.T) {
	mux, _ := newMux(t)

	patchW := httptest.NewRecorder()
	mux.ServeHTTP(patchW, httptest.NewRequest("PATCH", "/api/settings",
		bytes.NewBufferString(`{"foo":"bar"}`)))
	if patchW.Code != http.StatusOK {
		t.Fatalf("PATCH want 200, got %d: %s", patchW.Code, patchW.Body)
	}

	getW := httptest.NewRecorder()
	mux.ServeHTTP(getW, httptest.NewRequest("GET", "/api/settings", nil))
	var resp map[string]string
	json.NewDecoder(getW.Body).Decode(&resp)
	if resp["foo"] != "bar" {
		t.Errorf("want foo=bar, got %v", resp["foo"])
	}
}

func TestPatchSettingsMultipleKeys(t *testing.T) {
	mux, _ := newMux(t)

	patchW := httptest.NewRecorder()
	mux.ServeHTTP(patchW, httptest.NewRequest("PATCH", "/api/settings",
		bytes.NewBufferString(`{"theme":"dark","default_servings":"4"}`)))
	if patchW.Code != http.StatusOK {
		t.Fatalf("PATCH want 200, got %d: %s", patchW.Code, patchW.Body)
	}
	var resp map[string]string
	json.NewDecoder(patchW.Body).Decode(&resp)
	if resp["theme"] != "dark" {
		t.Errorf("want theme=dark, got %v", resp["theme"])
	}
	if resp["default_servings"] != "4" {
		t.Errorf("want default_servings=4, got %v", resp["default_servings"])
	}
}

func TestPatchSettingsInvalidDefaultServings(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("PATCH", "/api/settings",
		bytes.NewBufferString(`{"default_servings":"abc"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestPatchSettingsZeroDefaultServings(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("PATCH", "/api/settings",
		bytes.NewBufferString(`{"default_servings":"0"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestPatchSettingsUpsert(t *testing.T) {
	mux, _ := newMux(t)

	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PATCH", "/api/settings",
		bytes.NewBufferString(`{"mykey":"v1"}`)))
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PATCH", "/api/settings",
		bytes.NewBufferString(`{"mykey":"v2"}`)))

	getW := httptest.NewRecorder()
	mux.ServeHTTP(getW, httptest.NewRequest("GET", "/api/settings", nil))
	var resp map[string]string
	json.NewDecoder(getW.Body).Decode(&resp)
	if resp["mykey"] != "v2" {
		t.Errorf("want mykey=v2 (upserted), got %v", resp["mykey"])
	}
}

func TestPatchSettingsEmptyBody(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("PATCH", "/api/settings", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for empty body, got %d", w.Code)
	}
}

func TestGetBackup(t *testing.T) {
	mux, _ := newMux(t)
	t.Setenv("DB_PATH", "/home/josh/code_projects/kitchen_manager/kitchen.db")

	req := httptest.NewRequest("GET", "/api/backup", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	disposition := w.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "kitchen.db") {
		t.Errorf("want Content-Disposition to contain 'kitchen.db', got %q", disposition)
	}
}
