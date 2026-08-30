package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestDeleteShoppingItem(t *testing.T) {
	mux, _ := newMux(t)

	// Add item
	addW := httptest.NewRecorder()
	mux.ServeHTTP(addW, httptest.NewRequest("POST", "/api/shopping/",
		bytes.NewBufferString(`{"name":"DeleteMe","quantity_needed":1,"unit":""}`)))
	if addW.Code != http.StatusCreated {
		t.Fatalf("create item: want 201, got %d: %s", addW.Code, addW.Body)
	}
	var created map[string]any
	json.NewDecoder(addW.Body).Decode(&created)
	id := int64(created["id"].(float64))

	// Delete
	delW := httptest.NewRecorder()
	mux.ServeHTTP(delW, httptest.NewRequest("DELETE", "/api/shopping/"+strconv.FormatInt(id, 10), nil))
	if delW.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", delW.Code, delW.Body)
	}

	// Verify gone from list
	listW := httptest.NewRecorder()
	mux.ServeHTTP(listW, httptest.NewRequest("GET", "/api/shopping/", nil))
	var items []map[string]any
	json.NewDecoder(listW.Body).Decode(&items)
	for _, item := range items {
		if int64(item["id"].(float64)) == id {
			t.Error("deleted item should not appear in list")
		}
	}
}

func TestDeleteShoppingItemNotFound(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("DELETE", "/api/shopping/99999", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestGetShoppingListShowChecked(t *testing.T) {
	mux, _ := newMux(t)

	addItem := func(name string) int64 {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/shopping/",
			bytes.NewBufferString(`{"name":"`+name+`","quantity_needed":1,"unit":""}`)))
		var item map[string]any
		json.NewDecoder(w.Body).Decode(&item)
		return int64(item["id"].(float64))
	}
	id1 := addItem("UncheckedItem")
	id2 := addItem("CheckedItem")

	// Check id2
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PATCH", "/api/shopping/"+strconv.FormatInt(id2, 10),
		bytes.NewBufferString(`{"checked":true}`)))

	// Default GET (show_checked=false) → only unchecked
	defW := httptest.NewRecorder()
	mux.ServeHTTP(defW, httptest.NewRequest("GET", "/api/shopping/", nil))
	var defItems []map[string]any
	json.NewDecoder(defW.Body).Decode(&defItems)
	for _, item := range defItems {
		if int64(item["id"].(float64)) == id2 {
			t.Error("checked item should not appear in default list")
		}
	}
	foundUnchecked := false
	for _, item := range defItems {
		if int64(item["id"].(float64)) == id1 {
			foundUnchecked = true
		}
	}
	if !foundUnchecked {
		t.Error("unchecked item should appear in default list")
	}

	// show_checked=true → both items
	allW := httptest.NewRecorder()
	mux.ServeHTTP(allW, httptest.NewRequest("GET", "/api/shopping/?show_checked=true", nil))
	var allItems []map[string]any
	json.NewDecoder(allW.Body).Decode(&allItems)
	foundID1, foundID2 := false, false
	for _, item := range allItems {
		switch int64(item["id"].(float64)) {
		case id1:
			foundID1 = true
		case id2:
			foundID2 = true
		}
	}
	if !foundID1 {
		t.Error("unchecked item should appear with show_checked=true")
	}
	if !foundID2 {
		t.Error("checked item should appear with show_checked=true")
	}
}

func TestDeleteShoppingItemThenListShowChecked(t *testing.T) {
	mux, _ := newMux(t)

	// Add item
	addW := httptest.NewRecorder()
	mux.ServeHTTP(addW, httptest.NewRequest("POST", "/api/shopping/",
		bytes.NewBufferString(`{"name":"CheckThenDelete","quantity_needed":1,"unit":""}`)))
	var item map[string]any
	json.NewDecoder(addW.Body).Decode(&item)
	id := int64(item["id"].(float64))

	// Check it
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PATCH", "/api/shopping/"+strconv.FormatInt(id, 10),
		bytes.NewBufferString(`{"checked":true}`)))

	// Delete it
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("DELETE", "/api/shopping/"+strconv.FormatInt(id, 10), nil))

	// show_checked=true → should be empty (or at least not contain this item)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/shopping/?show_checked=true", nil))
	var items []map[string]any
	json.NewDecoder(w.Body).Decode(&items)
	for _, listItem := range items {
		if int64(listItem["id"].(float64)) == id {
			t.Error("deleted item should not appear even with show_checked=true")
		}
	}
}
