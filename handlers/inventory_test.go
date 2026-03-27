package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"kitchen_manager/handlers"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`PRAGMA foreign_keys = ON;
	CREATE TABLE IF NOT EXISTS inventory (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		quantity REAL NOT NULL DEFAULT 0,
		unit TEXT NOT NULL DEFAULT '',
		location TEXT NOT NULL DEFAULT '',
		expiration_date TEXT NOT NULL DEFAULT '',
		low_threshold REAL NOT NULL DEFAULT 1,
		barcode TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS shopping_list (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id INTEGER REFERENCES inventory(id),
		name TEXT NOT NULL,
		quantity_needed REAL NOT NULL DEFAULT 1,
		unit TEXT NOT NULL DEFAULT '',
		checked INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT 'manual'
	);
	CREATE TABLE IF NOT EXISTS recipes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		instructions TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '',
		servings INTEGER NOT NULL DEFAULT 1
	);
	CREATE TABLE IF NOT EXISTS recipe_ingredients (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		recipe_id INTEGER NOT NULL REFERENCES recipes(id),
		inventory_id INTEGER REFERENCES inventory(id),
		name TEXT NOT NULL,
		quantity REAL NOT NULL,
		unit TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS meal_calendar (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date TEXT NOT NULL,
		meal_slot TEXT NOT NULL DEFAULT 'dinner',
		recipe_id INTEGER NOT NULL REFERENCES recipes(id),
		servings INTEGER NOT NULL DEFAULT 1
	);`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	db := newTestDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterCalendar(mux, db)
	return mux, db
}

func TestCreateInventoryItem(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Milk","quantity":1,"unit":"gallon","location":"fridge","low_threshold":0.5}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var item map[string]any
	json.NewDecoder(w.Body).Decode(&item)
	if item["name"] != "Milk" {
		t.Errorf("want name Milk, got %v", item["name"])
	}
	if item["id"] == nil {
		t.Error("want id, got nil")
	}
}

func TestListInventoryItems(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Eggs","quantity":12,"unit":"count","location":"fridge"}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("GET", "/api/inventory/", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}
	var items []map[string]any
	json.NewDecoder(w2.Body).Decode(&items)
	found := false
	for _, item := range items {
		if item["name"] == "Eggs" {
			found = true
		}
	}
	if !found {
		t.Error("Eggs not found in inventory list")
	}
}

func TestUpdateInventoryItem(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(`{"name":"Butter","quantity":2,"unit":"stick","location":"fridge"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int(created["id"].(float64))

	patchBody := `{"quantity":1}`
	req2 := httptest.NewRequest("PATCH", "/api/inventory/"+strconv.Itoa(id), bytes.NewBufferString(patchBody))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w2.Code, w2.Body)
	}
	var updated map[string]any
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated["quantity"] != 1.0 {
		t.Errorf("want quantity 1, got %v", updated["quantity"])
	}
}

func TestDeleteInventoryItem(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(`{"name":"ToDelete","quantity":1,"unit":"","location":""}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int(created["id"].(float64))

	req2 := httptest.NewRequest("DELETE", "/api/inventory/"+strconv.Itoa(id), nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w2.Code)
	}

	req3 := httptest.NewRequest("GET", "/api/inventory/"+strconv.Itoa(id), nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("want 404 after delete, got %d", w3.Code)
	}
}

func TestGetExpiringSoon(t *testing.T) {
	mux, _ := newMux(t)
	expirationDate := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	body := `{"name":"Yogurt","quantity":1,"unit":"cup","location":"fridge","expiration_date":"` + expirationDate + `"}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("GET", "/api/inventory/expiring?days=7", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}
	var items []map[string]any
	json.NewDecoder(w2.Body).Decode(&items)
	found := false
	for _, item := range items {
		if item["name"] == "Yogurt" {
			found = true
		}
	}
	if !found {
		t.Error("Yogurt not found in expiring items")
	}
}

func TestAddManualShoppingItem(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Olive Oil","quantity_needed":1,"unit":"bottle","source":"manual"}`
	req := httptest.NewRequest("POST", "/api/shopping/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var item map[string]any
	json.NewDecoder(w.Body).Decode(&item)
	if item["name"] != "Olive Oil" {
		t.Errorf("want Olive Oil, got %v", item["name"])
	}
	if item["checked"] != false {
		t.Errorf("want checked=false, got %v", item["checked"])
	}
}

func TestCheckShoppingItem(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("POST", "/api/shopping/", bytes.NewBufferString(`{"name":"Pepper","quantity_needed":1,"unit":"jar"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int(created["id"].(float64))

	req2 := httptest.NewRequest("PATCH", "/api/shopping/"+strconv.Itoa(id), bytes.NewBufferString(`{"checked":true}`))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}
	var updated map[string]any
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated["checked"] != true {
		t.Errorf("want checked=true, got %v", updated["checked"])
	}
}

func TestClearCheckedShoppingItems(t *testing.T) {
	mux, _ := newMux(t)
	var id1, id2 int
	for i, name := range []string{"ItemA", "ItemB"} {
		req := httptest.NewRequest("POST", "/api/shopping/", bytes.NewBufferString(`{"name":"`+name+`","quantity_needed":1,"unit":""}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var c map[string]any
		json.NewDecoder(w.Body).Decode(&c)
		if i == 0 {
			id1 = int(c["id"].(float64))
		} else {
			id2 = int(c["id"].(float64))
			_ = id2
		}
	}
	// Check ItemA
	req := httptest.NewRequest("PATCH", "/api/shopping/"+strconv.Itoa(id1), bytes.NewBufferString(`{"checked":true}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("DELETE", "/api/shopping/checked", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}

	req3 := httptest.NewRequest("GET", "/api/shopping/", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	var items []map[string]any
	json.NewDecoder(w3.Body).Decode(&items)
	for _, item := range items {
		if item["name"] == "ItemA" {
			t.Error("ItemA should have been cleared")
		}
	}
	found := false
	for _, item := range items {
		if item["name"] == "ItemB" {
			found = true
		}
	}
	if !found {
		t.Error("ItemB should still be on the list")
	}
}

func TestGenerateFromThresholds(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"ThresholdTest","quantity":0.2,"unit":"bottle","location":"pantry","low_threshold":1.0}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("POST", "/api/shopping/generate-from-thresholds", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w2.Code, w2.Body)
	}

	req3 := httptest.NewRequest("GET", "/api/shopping/", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	var items []map[string]any
	json.NewDecoder(w3.Body).Decode(&items)
	found := false
	for _, item := range items {
		if item["name"] == "ThresholdTest" {
			found = true
		}
	}
	if !found {
		t.Error("ThresholdTest not found in shopping list after threshold generation")
	}
}
