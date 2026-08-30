package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kitchen_manager/handlers"
	_ "modernc.org/sqlite"
)

func newRecipeMux(t *testing.T) *http.ServeMux {
	t.Helper()
	db := newTestDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterCalendar(mux, db)
	handlers.RegisterRecipeImport(mux)
	return mux
}

func TestRecipeImport_HtmlPaste_JsonLd(t *testing.T) {
	mux := newRecipeMux(t)
	html := `<html><body><script type="application/ld+json">{
		"@type": "Recipe",
		"name": "JSON-LD Recipe",
		"description": "Test desc",
		"recipeYield": 2,
		"recipeInstructions": "Do stuff",
		"recipeIngredient": ["1 cup flour"]
	}</script></body></html>`
	req := httptest.NewRequest(http.MethodPost, "/api/recipes/import/html", strings.NewReader(html))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	recipe := result["recipe"].(map[string]any)
	if recipe["name"] != "JSON-LD Recipe" {
		t.Errorf("name = %v, want JSON-LD Recipe", recipe["name"])
	}
}

func TestRecipeImport_HtmlPaste_NoJsonLd(t *testing.T) {
	mux := newRecipeMux(t)
	html := `<html><body><h1>How to make a sandwich</h1><p>Step 1: Get bread.</p></body></html>`
	req := httptest.NewRequest(http.MethodPost, "/api/recipes/import/html", strings.NewReader(html))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if _, ok := result["text"]; !ok {
		t.Errorf("expected 'text' key in result, got keys: %v", getKeys(result))
	}
}

func TestRecipeImport_HtmlPaste_EmptyBody(t *testing.T) {
	mux := newRecipeMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/recipes/import/html", strings.NewReader(""))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for empty body, got %d", w.Code)
	}
}

func TestRecipeImport_UrlImport(t *testing.T) {
	mux := newRecipeMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/recipes/import/url",
		strings.NewReader(`{"url": "not-a-url"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid URL, got %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/recipes/import/url",
		strings.NewReader(`{"url": "file:///tmp/foo.html"}`))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("want 400 for file:// URL, got %d", w2.Code)
	}
}

func TestRecipeImport_UrlImport_InvalidJson(t *testing.T) {
	mux := newRecipeMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/recipes/import/url", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid JSON, got %d", w.Code)
	}
}

func TestRecipeImport_UrlImport_Empty(t *testing.T) {
	mux := newRecipeMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/recipes/import/url",
		strings.NewReader(`{"url": ""}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for empty URL, got %d", w.Code)
	}
}

func TestRecipeImport_PdfImport_MissingFile(t *testing.T) {
	mux := newRecipeMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/recipes/import/pdf", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing file, got %d", w.Code)
	}
}

func getKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
