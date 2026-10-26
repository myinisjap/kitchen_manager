package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestLogMealBasic(t *testing.T) {
	mux, db := newMux(t)

	// Create recipe
	res, err := db.Exec(`INSERT INTO recipes (name, servings) VALUES ('Pasta', 2)`)
	if err != nil {
		t.Fatal(err)
	}
	recipeID, _ := res.LastInsertId()

	body := `{"recipe_id":` + strconv.FormatInt(recipeID, 10) + `,"servings_made":2,"cooked_at":"2026-01-01T12:00:00Z","notes":"tasty"}`
	req := httptest.NewRequest("POST", "/api/meals", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["id"] == nil {
		t.Error("want id in response")
	}
	if resp["recipe_id"].(float64) != float64(recipeID) {
		t.Errorf("want recipe_id %d, got %v", recipeID, resp["recipe_id"])
	}
	if resp["servings_made"].(float64) != 2 {
		t.Errorf("want servings_made 2, got %v", resp["servings_made"])
	}
	if resp["notes"] != "tasty" {
		t.Errorf("want notes 'tasty', got %v", resp["notes"])
	}
}

func TestLogMealDeductsInventory(t *testing.T) {
	mux, _ := newMux(t)

	// Create inventory item via HTTP
	invW := httptest.NewRecorder()
	mux.ServeHTTP(invW, httptest.NewRequest("POST", "/api/inventory/",
		bytes.NewBufferString(`{"name":"Flour","quantity":10,"unit":"g"}`)))
	if invW.Code != http.StatusCreated {
		t.Fatalf("create item: want 201, got %d: %s", invW.Code, invW.Body)
	}
	var invItem map[string]any
	json.NewDecoder(invW.Body).Decode(&invItem)
	invID := int64(invItem["id"].(float64))

	// Create recipe with 4g per serving (recipe has 1 serving)
	recipeBody := `{"name":"FlourDish","servings":1,"ingredients":[{"name":"Flour","quantity":4,"unit":"g","inventory_id":` + strconv.FormatInt(invID, 10) + `}]}`
	recW := httptest.NewRecorder()
	mux.ServeHTTP(recW, httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(recipeBody)))
	if recW.Code != http.StatusCreated {
		t.Fatalf("create recipe: want 201, got %d: %s", recW.Code, recW.Body)
	}
	var rec map[string]any
	json.NewDecoder(recW.Body).Decode(&rec)
	recipeID := int64(rec["id"].(float64))

	// Cook 2 servings → should deduct 4*2=8, leaving 2
	mealBody := `{"recipe_id":` + strconv.FormatInt(recipeID, 10) + `,"servings_made":2}`
	mealW := httptest.NewRecorder()
	mux.ServeHTTP(mealW, httptest.NewRequest("POST", "/api/meals", bytes.NewBufferString(mealBody)))
	if mealW.Code != http.StatusCreated {
		t.Fatalf("log meal: want 201, got %d: %s", mealW.Code, mealW.Body)
	}

	// Check inventory
	getW := httptest.NewRecorder()
	mux.ServeHTTP(getW, httptest.NewRequest("GET", "/api/inventory/"+strconv.FormatInt(invID, 10), nil))
	var item map[string]any
	json.NewDecoder(getW.Body).Decode(&item)
	if item["quantity"].(float64) != 2 {
		t.Errorf("want quantity 2 after deduction, got %v", item["quantity"])
	}
}

func TestLogMealWithCost(t *testing.T) {
	mux, db := newMux(t)

	// Create inventory item with unit_cost_cents=100
	res, err := db.Exec(`INSERT INTO inventory (name, quantity, unit, unit_cost_cents, low_threshold) VALUES ('butter', 100, 'g', 100, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	invID, _ := res.LastInsertId()

	// Recipe: 2g per 1 serving
	res2, err := db.Exec(`INSERT INTO recipes (name, servings) VALUES ('ButterDish', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	recipeID, _ := res2.LastInsertId()
	_, err = db.Exec(`INSERT INTO recipe_ingredients (recipe_id, inventory_id, name, quantity, unit) VALUES (?, ?, 'butter', 2, 'g')`, recipeID, invID)
	if err != nil {
		t.Fatal(err)
	}

	// Cook 2 servings → 2g*2=4g used, cost=4*100=400 cents
	body := `{"recipe_id":` + strconv.FormatInt(recipeID, 10) + `,"servings_made":2}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/meals", bytes.NewBufferString(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total_cost_cents"].(float64) != 400 {
		t.Errorf("want total_cost_cents 400, got %v", resp["total_cost_cents"])
	}
}

func TestLogMealRecipeNotFound(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"recipe_id":99999,"servings_made":1}`
	req := httptest.NewRequest("POST", "/api/meals", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestLogMealInvalidBody(t *testing.T) {
	mux, _ := newMux(t)
	// Missing recipe_id (zero value)
	body := `{"servings_made":1}`
	req := httptest.NewRequest("POST", "/api/meals", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestLogMealDefaultServings(t *testing.T) {
	mux, db := newMux(t)

	// Recipe with servings=3
	res, err := db.Exec(`INSERT INTO recipes (name, servings) VALUES ('BigBatch', 3)`)
	if err != nil {
		t.Fatal(err)
	}
	recipeID, _ := res.LastInsertId()

	// Post without servings_made → should default to recipe servings (3)
	body := `{"recipe_id":` + strconv.FormatInt(recipeID, 10) + `}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/meals", bytes.NewBufferString(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["servings_made"].(float64) != 3 {
		t.Errorf("want servings_made 3 (default), got %v", resp["servings_made"])
	}
}

func TestListMeals(t *testing.T) {
	mux, db := newMux(t)

	res, err := db.Exec(`INSERT INTO recipes (name, servings) VALUES ('Soup', 2)`)
	if err != nil {
		t.Fatal(err)
	}
	recipeID, _ := res.LastInsertId()

	// Log 3 meals
	for i := 0; i < 3; i++ {
		body := `{"recipe_id":` + strconv.FormatInt(recipeID, 10) + `,"servings_made":1}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/meals", bytes.NewBufferString(body)))
		if w.Code != http.StatusCreated {
			t.Fatalf("log meal %d: want 201, got %d", i, w.Code)
		}
	}

	req := httptest.NewRequest("GET", "/api/meals", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total"].(float64) < 3 {
		t.Errorf("want total >= 3, got %v", resp["total"])
	}
	meals := resp["meals"].([]any)
	if len(meals) < 3 {
		t.Errorf("want >= 3 meals, got %d", len(meals))
	}
	// Verify ingredients array is present
	firstMeal := meals[0].(map[string]any)
	if _, ok := firstMeal["ingredients"]; !ok {
		t.Error("want ingredients array in meal response")
	}
}

func TestListMealsFilterByRecipeID(t *testing.T) {
	mux, db := newMux(t)

	res1, _ := db.Exec(`INSERT INTO recipes (name, servings) VALUES ('RecipeA', 1)`)
	id1, _ := res1.LastInsertId()
	res2, _ := db.Exec(`INSERT INTO recipes (name, servings) VALUES ('RecipeB', 1)`)
	id2, _ := res2.LastInsertId()

	logMeal := func(recipeID int64) {
		body := `{"recipe_id":` + strconv.FormatInt(recipeID, 10) + `,"servings_made":1}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/meals", bytes.NewBufferString(body)))
	}
	logMeal(id1)
	logMeal(id1)
	logMeal(id2)

	req := httptest.NewRequest("GET", "/api/meals?recipe_id="+strconv.FormatInt(id1, 10), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total"].(float64) != 2 {
		t.Errorf("want total=2 for recipe_id filter, got %v", resp["total"])
	}
}

func TestListMealsPagination(t *testing.T) {
	mux, db := newMux(t)

	res, _ := db.Exec(`INSERT INTO recipes (name, servings) VALUES ('PagRecipe', 1)`)
	recipeID, _ := res.LastInsertId()

	for i := 0; i < 5; i++ {
		body := `{"recipe_id":` + strconv.FormatInt(recipeID, 10) + `,"servings_made":1}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/meals", bytes.NewBufferString(body)))
	}

	req := httptest.NewRequest("GET", "/api/meals?limit=2&offset=2", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	meals := resp["meals"].([]any)
	if len(meals) != 2 {
		t.Errorf("want 2 meals with limit=2&offset=2, got %d", len(meals))
	}
}

func TestMealsStats(t *testing.T) {
	mux, db := newMux(t)

	res, _ := db.Exec(`INSERT INTO recipes (name, servings) VALUES ('StatsRecipe', 1)`)
	recipeID, _ := res.LastInsertId()

	// Insert 2 meals with cooked_at this week (use a fixed recent timestamp)
	for i := 0; i < 2; i++ {
		body := `{"recipe_id":` + strconv.FormatInt(recipeID, 10) + `,"servings_made":1}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/meals", bytes.NewBufferString(body)))
		if w.Code != http.StatusCreated {
			t.Fatalf("log meal: want 201, got %d: %s", w.Code, w.Body)
		}
	}

	req := httptest.NewRequest("GET", "/api/meals/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total_meals_this_week"].(float64) < 2 {
		t.Errorf("want total_meals_this_week >= 2, got %v", resp["total_meals_this_week"])
	}
}

func TestMealsStatsEmpty(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("GET", "/api/meals/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total_meals_this_week"].(float64) != 0 {
		t.Errorf("want 0 meals this week, got %v", resp["total_meals_this_week"])
	}
	mostCooked := resp["most_cooked"].([]any)
	if len(mostCooked) != 0 {
		t.Errorf("want empty most_cooked, got %d", len(mostCooked))
	}
}
