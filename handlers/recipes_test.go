package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestGetRecipeByID(t *testing.T) {
	mux, _ := newMux(t)

	createW := httptest.NewRecorder()
	mux.ServeHTTP(createW, httptest.NewRequest("POST", "/api/recipes/",
		bytes.NewBufferString(`{"name":"Omelette","servings":1,"ingredients":[{"name":"egg","quantity":2,"unit":"piece"}]}`)))
	if createW.Code != http.StatusCreated {
		t.Fatalf("create recipe: want 201, got %d: %s", createW.Code, createW.Body)
	}
	var created map[string]any
	json.NewDecoder(createW.Body).Decode(&created)
	id := int(created["id"].(float64))

	req := httptest.NewRequest("GET", "/api/recipes/"+strconv.Itoa(id), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var recipe map[string]any
	json.NewDecoder(w.Body).Decode(&recipe)
	if recipe["name"] != "Omelette" {
		t.Errorf("want name Omelette, got %v", recipe["name"])
	}
	ings := recipe["ingredients"].([]any)
	if len(ings) != 1 {
		t.Errorf("want 1 ingredient, got %d", len(ings))
	}
}

func TestGetRecipeByIDNotFound(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("GET", "/api/recipes/99999", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestUpdateRecipe(t *testing.T) {
	mux, _ := newMux(t)

	createW := httptest.NewRecorder()
	mux.ServeHTTP(createW, httptest.NewRequest("POST", "/api/recipes/",
		bytes.NewBufferString(`{"name":"Stew","servings":2,"ingredients":[{"name":"beef","quantity":200,"unit":"g"},{"name":"potato","quantity":100,"unit":"g"}]}`)))
	var created map[string]any
	json.NewDecoder(createW.Body).Decode(&created)
	id := int(created["id"].(float64))

	// Update to 1 ingredient
	putBody := `{"name":"Stew","servings":2,"ingredients":[{"name":"beef","quantity":300,"unit":"g"}]}`
	req := httptest.NewRequest("PUT", "/api/recipes/"+strconv.Itoa(id), bytes.NewBufferString(putBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var updated map[string]any
	json.NewDecoder(w.Body).Decode(&updated)
	ings := updated["ingredients"].([]any)
	if len(ings) != 1 {
		t.Errorf("want 1 ingredient after update, got %d", len(ings))
	}
}

func TestUpdateRecipeNotFound(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Ghost","servings":1,"ingredients":[]}`
	req := httptest.NewRequest("PUT", "/api/recipes/99999", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestUpdateRecipeInvalidUnit(t *testing.T) {
	mux, _ := newMux(t)

	createW := httptest.NewRecorder()
	mux.ServeHTTP(createW, httptest.NewRequest("POST", "/api/recipes/",
		bytes.NewBufferString(`{"name":"BadUnit","servings":1,"ingredients":[]}`)))
	var created map[string]any
	json.NewDecoder(createW.Body).Decode(&created)
	id := int(created["id"].(float64))

	putBody := `{"name":"BadUnit","servings":1,"ingredients":[{"name":"flour","quantity":1,"unit":"handful"}]}`
	req := httptest.NewRequest("PUT", "/api/recipes/"+strconv.Itoa(id), bytes.NewBufferString(putBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestDeleteRecipe(t *testing.T) {
	mux, _ := newMux(t)

	createW := httptest.NewRecorder()
	mux.ServeHTTP(createW, httptest.NewRequest("POST", "/api/recipes/",
		bytes.NewBufferString(`{"name":"ToDelete","servings":1,"ingredients":[]}`)))
	var created map[string]any
	json.NewDecoder(createW.Body).Decode(&created)
	id := int(created["id"].(float64))

	delReq := httptest.NewRequest("DELETE", "/api/recipes/"+strconv.Itoa(id), nil)
	delW := httptest.NewRecorder()
	mux.ServeHTTP(delW, delReq)
	if delW.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", delW.Code)
	}

	getReq := httptest.NewRequest("GET", "/api/recipes/"+strconv.Itoa(id), nil)
	getW := httptest.NewRecorder()
	mux.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusNotFound {
		t.Errorf("want 404 after delete, got %d", getW.Code)
	}
}

func TestDeleteRecipeClearsCalendar(t *testing.T) {
	mux, db := newMux(t)

	createW := httptest.NewRecorder()
	mux.ServeHTTP(createW, httptest.NewRequest("POST", "/api/recipes/",
		bytes.NewBufferString(`{"name":"CalRecipeToDelete","servings":1,"ingredients":[]}`)))
	var created map[string]any
	json.NewDecoder(createW.Body).Decode(&created)
	recipeID := int64(created["id"].(float64))

	// Add to calendar
	_, err := db.Exec(`INSERT INTO meal_calendar (date, meal_slot, recipe_id, servings) VALUES ('2030-06-01', 'dinner', ?, 1)`, recipeID)
	if err != nil {
		t.Fatal(err)
	}

	delW := httptest.NewRecorder()
	mux.ServeHTTP(delW, httptest.NewRequest("DELETE", "/api/recipes/"+strconv.FormatInt(recipeID, 10), nil))
	if delW.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", delW.Code)
	}

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM meal_calendar WHERE recipe_id=?`, recipeID).Scan(&count)
	if count != 0 {
		t.Errorf("want 0 calendar entries after recipe delete, got %d", count)
	}
}

func TestDeleteRecipeNotFound(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("DELETE", "/api/recipes/99999", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestGetRecipeFutureCalendar(t *testing.T) {
	mux, db := newMux(t)

	createW := httptest.NewRecorder()
	mux.ServeHTTP(createW, httptest.NewRequest("POST", "/api/recipes/",
		bytes.NewBufferString(`{"name":"FutureRecipe","servings":1,"ingredients":[]}`)))
	var created map[string]any
	json.NewDecoder(createW.Body).Decode(&created)
	recipeID := int64(created["id"].(float64))

	// One future, one past
	_, err := db.Exec(`INSERT INTO meal_calendar (date, meal_slot, recipe_id, servings) VALUES ('2030-01-01', 'dinner', ?, 1)`, recipeID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO meal_calendar (date, meal_slot, recipe_id, servings) VALUES ('2020-01-01', 'dinner', ?, 1)`, recipeID)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/recipes/"+strconv.FormatInt(recipeID, 10)+"/future-calendar", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 1 {
		t.Errorf("want count=1 (only future), got %v", resp["count"])
	}
}

func TestGetRecipeFutureCalendarEmpty(t *testing.T) {
	mux, _ := newMux(t)

	createW := httptest.NewRecorder()
	mux.ServeHTTP(createW, httptest.NewRequest("POST", "/api/recipes/",
		bytes.NewBufferString(`{"name":"NoCalRecipe","servings":1,"ingredients":[]}`)))
	var created map[string]any
	json.NewDecoder(createW.Body).Decode(&created)
	id := int(created["id"].(float64))

	req := httptest.NewRequest("GET", "/api/recipes/"+strconv.Itoa(id)+"/future-calendar", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 0 {
		t.Errorf("want count=0, got %v", resp["count"])
	}
}
