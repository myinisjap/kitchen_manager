package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func createCalendarRecipe(t *testing.T, mux interface{ ServeHTTP(http.ResponseWriter, *http.Request) }, name string) int64 {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/recipes/",
		bytes.NewBufferString(`{"name":"`+name+`","servings":1,"ingredients":[]}`)))
	if w.Code != http.StatusCreated {
		t.Fatalf("createCalendarRecipe %s: want 201, got %d: %s", name, w.Code, w.Body)
	}
	var rec map[string]any
	json.NewDecoder(w.Body).Decode(&rec)
	return int64(rec["id"].(float64))
}

func addToCalendar(t *testing.T, mux interface{ ServeHTTP(http.ResponseWriter, *http.Request) }, recipeID int64, date string) int64 {
	t.Helper()
	body := `{"date":"` + date + `","meal_slot":"dinner","recipe_id":` + strconv.FormatInt(recipeID, 10) + `,"servings":1}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/calendar/", bytes.NewBufferString(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("addToCalendar %s: want 201, got %d: %s", date, w.Code, w.Body)
	}
	var entry map[string]any
	json.NewDecoder(w.Body).Decode(&entry)
	return int64(entry["id"].(float64))
}

func TestGetCalendarWeek(t *testing.T) {
	mux, _ := newMux(t)
	recipeID := createCalendarRecipe(t, mux, "WeekRecipe")

	// 3 entries: 2 in week1, 1 in week2
	addToCalendar(t, mux, recipeID, "2030-03-10")
	addToCalendar(t, mux, recipeID, "2030-03-11")
	addToCalendar(t, mux, recipeID, "2030-03-18") // next week

	req := httptest.NewRequest("GET", "/api/calendar/week?start=2030-03-10", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var entries []map[string]any
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 2 {
		t.Errorf("want 2 entries for week, got %d", len(entries))
	}
}

func TestGetCalendarWeekEmpty(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("GET", "/api/calendar/week?start=2030-01-01", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var entries []map[string]any
	json.NewDecoder(w.Body).Decode(&entries)
	if len(entries) != 0 {
		t.Errorf("want empty array, got %d entries", len(entries))
	}
}

func TestGetCalendarWeekInvalidDate(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("GET", "/api/calendar/week?start=notadate", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestDeleteCalendarEntry(t *testing.T) {
	mux, _ := newMux(t)
	recipeID := createCalendarRecipe(t, mux, "DelCalRecipe")
	entryID := addToCalendar(t, mux, recipeID, "2030-05-01")

	delReq := httptest.NewRequest("DELETE", "/api/calendar/"+strconv.FormatInt(entryID, 10), nil)
	delW := httptest.NewRecorder()
	mux.ServeHTTP(delW, delReq)
	if delW.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", delW.Code)
	}

	// Confirm gone from week view
	getReq := httptest.NewRequest("GET", "/api/calendar/week?start=2030-04-28", nil)
	getW := httptest.NewRecorder()
	mux.ServeHTTP(getW, getReq)
	var entries []map[string]any
	json.NewDecoder(getW.Body).Decode(&entries)
	if len(entries) != 0 {
		t.Errorf("want empty week after delete, got %d entries", len(entries))
	}
}

func TestDeleteCalendarEntryNotFound(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("DELETE", "/api/calendar/99999", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestAddToCalendarInvalidDate(t *testing.T) {
	mux, _ := newMux(t)
	recipeID := createCalendarRecipe(t, mux, "InvalidDateRecipe")
	body := `{"date":"not-a-date","meal_slot":"dinner","recipe_id":` + strconv.FormatInt(recipeID, 10) + `,"servings":1}`
	req := httptest.NewRequest("POST", "/api/calendar/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAddToCalendarRecipeNotFound(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"date":"2030-01-01","meal_slot":"dinner","recipe_id":99999,"servings":1}`
	req := httptest.NewRequest("POST", "/api/calendar/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}
