package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestGetHistoryEmpty(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("GET", "/api/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total"].(float64) != 0 {
		t.Errorf("want total=0, got %v", resp["total"])
	}
	rows := resp["rows"].([]any)
	if len(rows) != 0 {
		t.Errorf("want empty rows, got %d", len(rows))
	}
}

func TestGetHistoryAfterInventoryChange(t *testing.T) {
	mux, _ := newMux(t)

	// Create item
	createW := httptest.NewRecorder()
	mux.ServeHTTP(createW, httptest.NewRequest("POST", "/api/inventory/",
		bytes.NewBufferString(`{"name":"HistItem","quantity":10,"unit":"g"}`)))
	if createW.Code != http.StatusCreated {
		t.Fatalf("create item: want 201, got %d: %s", createW.Code, createW.Body)
	}
	var created map[string]any
	json.NewDecoder(createW.Body).Decode(&created)
	id := int(created["id"].(float64))

	// Patch quantity to trigger a history entry
	patchW := httptest.NewRecorder()
	mux.ServeHTTP(patchW, httptest.NewRequest("PATCH", "/api/inventory/"+strconv.Itoa(id),
		bytes.NewBufferString(`{"quantity":5}`)))
	if patchW.Code != http.StatusOK {
		t.Fatalf("patch item: want 200, got %d: %s", patchW.Code, patchW.Body)
	}

	req := httptest.NewRequest("GET", "/api/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total"].(float64) < 1 {
		t.Errorf("want total >= 1 after inventory change, got %v", resp["total"])
	}
	rows := resp["rows"].([]any)
	if len(rows) == 0 {
		t.Error("want at least 1 history row")
	}
	row := rows[0].(map[string]any)
	if row["inventory_id"] == nil {
		t.Error("want inventory_id in history row")
	}
	if row["change_type"] == nil {
		t.Error("want change_type in history row")
	}
}

func TestGetHistoryPagination(t *testing.T) {
	mux, db := newMux(t)

	// Insert inventory item and 5 history rows directly
	res, err := db.Exec(`INSERT INTO inventory (name, quantity, unit, low_threshold) VALUES ('pagitem', 1, 'g', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	invID, _ := res.LastInsertId()

	for i := 0; i < 5; i++ {
		_, err = db.Exec(`INSERT INTO inventory_history (inventory_id, item_name, changed_at, change_type, quantity_before, quantity_after, unit) VALUES (?, 'pagitem', datetime('now', ?||' seconds'), 'edit', 10, 9, 'g')`,
			invID, strconv.Itoa(i))
		if err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest("GET", "/api/history?limit=2&offset=1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total"].(float64) < 5 {
		t.Errorf("want total >= 5, got %v", resp["total"])
	}
	rows := resp["rows"].([]any)
	if len(rows) != 2 {
		t.Errorf("want 2 rows with limit=2, got %d", len(rows))
	}
}

func TestGetHistoryForItem(t *testing.T) {
	mux, _ := newMux(t)

	// Create two items
	createItem := func(name string) int {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/inventory/",
			bytes.NewBufferString(`{"name":"`+name+`","quantity":10,"unit":"g"}`)))
		var item map[string]any
		json.NewDecoder(w.Body).Decode(&item)
		return int(item["id"].(float64))
	}
	idA := createItem("ItemForHistA")
	idB := createItem("ItemForHistB")

	// Patch both items to generate history
	patch := func(id int) {
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PATCH", "/api/inventory/"+strconv.Itoa(id),
			bytes.NewBufferString(`{"quantity":5}`)))
	}
	patch(idA)
	patch(idB)

	req := httptest.NewRequest("GET", "/api/history/item/"+strconv.Itoa(idA), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var rows []map[string]any
	json.NewDecoder(w.Body).Decode(&rows)
	for _, row := range rows {
		if int(row["inventory_id"].(float64)) != idA {
			t.Errorf("want only rows for item %d, got inventory_id=%v", idA, row["inventory_id"])
		}
	}
	if len(rows) == 0 {
		t.Error("want at least 1 history row for item A")
	}
}

func TestGetHistoryForItemEmpty(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("GET", "/api/history/item/99999", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var rows []map[string]any
	json.NewDecoder(w.Body).Decode(&rows)
	if len(rows) != 0 {
		t.Errorf("want empty array for unknown item, got %d rows", len(rows))
	}
}

func TestGetHistoryForItemLimit(t *testing.T) {
	mux, db := newMux(t)

	res, err := db.Exec(`INSERT INTO inventory (name, quantity, unit, low_threshold) VALUES ('limititem', 1, 'g', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	invID, _ := res.LastInsertId()

	for i := 0; i < 10; i++ {
		_, err = db.Exec(`INSERT INTO inventory_history (inventory_id, item_name, changed_at, change_type, quantity_before, quantity_after, unit) VALUES (?, 'limititem', datetime('now', ?||' seconds'), 'edit', 10, 9, 'g')`,
			invID, strconv.Itoa(i))
		if err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest("GET", "/api/history/item/"+strconv.FormatInt(invID, 10)+"?limit=3", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var rows []map[string]any
	json.NewDecoder(w.Body).Decode(&rows)
	if len(rows) != 3 {
		t.Errorf("want 3 rows with limit=3, got %d", len(rows))
	}
}

func TestGetHistoryStats(t *testing.T) {
	mux, db := newMux(t)

	// Insert 3 items with varying change counts
	insertItem := func(name string) int64 {
		res, err := db.Exec(`INSERT INTO inventory (name, quantity, unit, low_threshold) VALUES (?, 1, 'g', 0)`, name)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	idA := insertItem("statsA")
	idB := insertItem("statsB")
	idC := insertItem("statsC")

	insertHistory := func(invID int64, name string, count int) {
		for i := 0; i < count; i++ {
			_, err := db.Exec(`INSERT INTO inventory_history (inventory_id, item_name, changed_at, change_type, unit) VALUES (?, ?, datetime('now'), 'edit', 'g')`,
				invID, name)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	insertHistory(idA, "statsA", 5)
	insertHistory(idB, "statsB", 3)
	insertHistory(idC, "statsC", 1)

	req := httptest.NewRequest("GET", "/api/history/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var stats []map[string]any
	json.NewDecoder(w.Body).Decode(&stats)
	if len(stats) == 0 {
		t.Fatal("want at least 1 stat entry")
	}
	// First entry should have the highest change_count (statsA with 5)
	firstCount := stats[0]["change_count"].(float64)
	for _, s := range stats[1:] {
		if s["change_count"].(float64) > firstCount {
			t.Error("want stats ordered by change_count DESC")
		}
	}
}

func TestGetHistoryStatsEmpty(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("GET", "/api/history/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var stats []map[string]any
	json.NewDecoder(w.Body).Decode(&stats)
	if len(stats) != 0 {
		t.Errorf("want empty stats on empty DB, got %d entries", len(stats))
	}
}
