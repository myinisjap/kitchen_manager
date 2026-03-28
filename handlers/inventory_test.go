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
		barcode TEXT NOT NULL DEFAULT '',
		preferred_unit TEXT NOT NULL DEFAULT ''
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

func TestPreferredUnitColumnExists(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.Query(`PRAGMA table_info(inventory)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, colType, notNull string
		var dfltValue, pk interface{}
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "preferred_unit" {
			found = true
		}
	}
	if !found {
		t.Error("inventory table missing preferred_unit column")
	}
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
	body := `{"name":"Milk","quantity":1,"unit":"L","location":"fridge","low_threshold":0.5}`
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
	body := `{"name":"Eggs","quantity":12,"unit":"piece","location":"fridge"}`
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
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(`{"name":"Butter","quantity":2,"unit":"oz","location":"fridge"}`))
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
	body := `{"name":"Yogurt","quantity":1,"unit":"ml","location":"fridge","expiration_date":"` + expirationDate + `"}`
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
	body := `{"name":"Olive Oil","quantity_needed":1,"unit":"L","source":"manual"}`
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
	var id1 int
	for i, name := range []string{"ItemA", "ItemB"} {
		req := httptest.NewRequest("POST", "/api/shopping/", bytes.NewBufferString(`{"name":"`+name+`","quantity_needed":1,"unit":""}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var c map[string]any
		json.NewDecoder(w.Body).Decode(&c)
		if i == 0 {
			id1 = int(c["id"].(float64))
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
	body := `{"name":"ThresholdTest","quantity":0.2,"unit":"L","location":"pantry","low_threshold":1.0}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("POST", "/api/shopping/generate-from-thresholds", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w2.Code, w2.Body)
	}
	var genResult map[string]any
	json.NewDecoder(w2.Body).Decode(&genResult)
	if genResult["added"] != 1.0 {
		t.Errorf("want added=1, got %v", genResult["added"])
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

func TestGenerateFromThresholdsIdempotent(t *testing.T) {
	mux, _ := newMux(t)
	// Create item below threshold
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/inventory/",
		bytes.NewBufferString(`{"name":"IdempTest","quantity":0.1,"unit":"kg","location":"pantry","low_threshold":1.0}`)))

	// First call — should add 1
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, httptest.NewRequest("POST", "/api/shopping/generate-from-thresholds", nil))
	var r1 map[string]any
	json.NewDecoder(w1.Body).Decode(&r1)
	if r1["added"] != 1.0 {
		t.Errorf("first call: want added=1, got %v", r1["added"])
	}

	// Second call — item already on unchecked list, should add 0
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, httptest.NewRequest("POST", "/api/shopping/generate-from-thresholds", nil))
	var r2 map[string]any
	json.NewDecoder(w2.Body).Decode(&r2)
	if r2["added"] != 0.0 {
		t.Errorf("second call: want added=0 (idempotent), got %v", r2["added"])
	}
}

func TestCreateRecipe(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Scrambled Eggs","description":"Simple breakfast","instructions":"Beat eggs, cook.","tags":"breakfast,quick","servings":2,"ingredients":[{"name":"Eggs","quantity":3,"unit":"piece"},{"name":"Butter","quantity":1,"unit":"tbsp"}]}`
	req := httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var recipe map[string]any
	json.NewDecoder(w.Body).Decode(&recipe)
	if recipe["name"] != "Scrambled Eggs" {
		t.Errorf("want Scrambled Eggs, got %v", recipe["name"])
	}
	ings := recipe["ingredients"].([]any)
	if len(ings) != 2 {
		t.Errorf("want 2 ingredients, got %d", len(ings))
	}
}

func TestFilterRecipesByTag(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Pancakes","tags":"breakfast,sweet","servings":4,"ingredients":[]}`
	req := httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("GET", "/api/recipes/?tag=breakfast", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	var recipes []map[string]any
	json.NewDecoder(w2.Body).Decode(&recipes)
	found := false
	for _, r := range recipes {
		if r["name"] == "Pancakes" {
			found = true
		}
	}
	if !found {
		t.Error("Pancakes not found when filtering by breakfast tag")
	}
}

func TestAddRecipeToShoppingList(t *testing.T) {
	mux, _ := newMux(t)
	// Create inventory item with 0 quantity
	invW := httptest.NewRecorder()
	mux.ServeHTTP(invW, httptest.NewRequest("POST", "/api/inventory/",
		bytes.NewBufferString(`{"name":"RecipeEgg","quantity":0,"unit":"piece","location":"fridge"}`)))
	var invItem map[string]any
	json.NewDecoder(invW.Body).Decode(&invItem)
	invID := int(invItem["id"].(float64))

	body := `{"name":"QuickOmelet","servings":1,"ingredients":[{"name":"RecipeEgg","quantity":2,"unit":"piece","inventory_id":` + strconv.Itoa(invID) + `}]}`
	req := httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var recipe map[string]any
	json.NewDecoder(w.Body).Decode(&recipe)
	recipeID := int(recipe["id"].(float64))

	req2 := httptest.NewRequest("POST", "/api/recipes/"+strconv.Itoa(recipeID)+"/add-to-shopping-list", nil)
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
		if item["name"] == "RecipeEgg" {
			found = true
		}
	}
	if !found {
		t.Error("RecipeEgg not found in shopping list after adding recipe")
	}
}

func TestAddMealToCalendar(t *testing.T) {
	mux, _ := newMux(t)
	recipeW := httptest.NewRecorder()
	mux.ServeHTTP(recipeW, httptest.NewRequest("POST", "/api/recipes/",
		bytes.NewBufferString(`{"name":"CalRecipe","servings":2,"ingredients":[]}`)))
	var rec map[string]any
	json.NewDecoder(recipeW.Body).Decode(&rec)
	recipeID := int(rec["id"].(float64))

	body := `{"date":"2026-04-07","meal_slot":"dinner","recipe_id":` + strconv.Itoa(recipeID) + `,"servings":2}`
	req := httptest.NewRequest("POST", "/api/calendar/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var entry map[string]any
	json.NewDecoder(w.Body).Decode(&entry)
	if entry["recipe_id"] != float64(recipeID) {
		t.Errorf("want recipe_id %d, got %v", recipeID, entry["recipe_id"])
	}
}

func TestFullWeeklyPlanningFlow(t *testing.T) {
	mux, _ := newMux(t)

	// Step 1: Add pasta to inventory (above threshold — should NOT appear in threshold shopping)
	pastaW := httptest.NewRecorder()
	mux.ServeHTTP(pastaW, httptest.NewRequest("POST", "/api/inventory/",
		bytes.NewBufferString(`{"name":"Pasta","quantity":500,"unit":"g","location":"Pantry","low_threshold":200}`)))
	if pastaW.Code != http.StatusCreated {
		t.Fatalf("create pasta: want 201, got %d: %s", pastaW.Code, pastaW.Body)
	}
	var pastaItem map[string]any
	json.Unmarshal(pastaW.Body.Bytes(), &pastaItem)
	pastaID := int(pastaItem["id"].(float64))

	// Step 2: Add sauce to inventory (below threshold — SHOULD appear in threshold shopping)
	sauceW := httptest.NewRecorder()
	mux.ServeHTTP(sauceW, httptest.NewRequest("POST", "/api/inventory/",
		bytes.NewBufferString(`{"name":"Tomato Sauce","quantity":1,"unit":"can","location":"Pantry","low_threshold":2}`)))
	if sauceW.Code != http.StatusCreated {
		t.Fatalf("create sauce: want 201, got %d: %s", sauceW.Code, sauceW.Body)
	}
	var sauceItem map[string]any
	json.Unmarshal(sauceW.Body.Bytes(), &sauceItem)
	sauceID := int(sauceItem["id"].(float64))

	// Step 3: Generate shopping from thresholds — verify exactly 1 item (sauce), not pasta
	threshW := httptest.NewRecorder()
	mux.ServeHTTP(threshW, httptest.NewRequest("POST", "/api/shopping/generate-from-thresholds", nil))
	if threshW.Code != http.StatusOK {
		t.Fatalf("generate-from-thresholds: want 200, got %d: %s", threshW.Code, threshW.Body)
	}
	var threshResult map[string]any
	json.Unmarshal(threshW.Body.Bytes(), &threshResult)
	if threshResult["added"] != 1.0 {
		t.Errorf("threshold generation: want added=1 (sauce only), got %v", threshResult["added"])
	}

	// Verify sauce is in list, pasta is not
	listW := httptest.NewRecorder()
	mux.ServeHTTP(listW, httptest.NewRequest("GET", "/api/shopping/", nil))
	var shoppingItems []map[string]any
	json.Unmarshal(listW.Body.Bytes(), &shoppingItems)
	foundSauce, foundPasta := false, false
	for _, item := range shoppingItems {
		if item["name"] == "Tomato Sauce" {
			foundSauce = true
		}
		if item["name"] == "Pasta" {
			foundPasta = true
		}
	}
	if !foundSauce {
		t.Error("threshold generation: Tomato Sauce should be in shopping list (below threshold)")
	}
	if foundPasta {
		t.Error("threshold generation: Pasta should NOT be in shopping list (above threshold)")
	}

	// Step 4: Create recipe "Pasta with Sauce"
	recipeBody := `{"name":"Pasta with Sauce","tags":"dinner","servings":2,"ingredients":[` +
		`{"name":"Pasta","quantity":300,"unit":"g","inventory_id":` + strconv.Itoa(pastaID) + `},` +
		`{"name":"Tomato Sauce","quantity":2,"unit":"can","inventory_id":` + strconv.Itoa(sauceID) + `}` +
		`]}`
	recipeW := httptest.NewRecorder()
	mux.ServeHTTP(recipeW, httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(recipeBody)))
	if recipeW.Code != http.StatusCreated {
		t.Fatalf("create recipe: want 201, got %d: %s", recipeW.Code, recipeW.Body)
	}
	var recipe map[string]any
	json.Unmarshal(recipeW.Body.Bytes(), &recipe)
	recipeID := int(recipe["id"].(float64))

	// Step 5: Add recipe to calendar on 2026-04-20 (Monday) for 2 servings
	for _, date := range []string{"2026-04-20", "2026-04-22"} {
		entry := `{"date":"` + date + `","meal_slot":"dinner","recipe_id":` + strconv.Itoa(recipeID) + `,"servings":2}`
		calW := httptest.NewRecorder()
		mux.ServeHTTP(calW, httptest.NewRequest("POST", "/api/calendar/", bytes.NewBufferString(entry)))
		if calW.Code != http.StatusCreated {
			t.Fatalf("add to calendar %s: want 201, got %d: %s", date, calW.Code, calW.Body)
		}
	}

	// Step 6: Generate weekly shopping for week of 2026-04-20
	weekW := httptest.NewRecorder()
	mux.ServeHTTP(weekW, httptest.NewRequest("POST", "/api/calendar/generate-weekly-shopping?start=2026-04-20", nil))
	if weekW.Code != http.StatusOK {
		t.Fatalf("generate-weekly-shopping: want 200, got %d: %s", weekW.Code, weekW.Body)
	}

	var weekResult map[string]any
	json.Unmarshal(weekW.Body.Bytes(), &weekResult)
	weekItems := weekResult["items"].([]any)

	// Verify math:
	// Pasta: 500g available. Recipe needs 300g per 2 servings (scale=1.0 since servings=2, recipe.servings=2).
	//   Monday: need 300g, have 500g → shortfall 0, simulated → 200g
	//   Wednesday: need 300g, have 200g → shortfall 100g, simulated → 0g
	//   Total pasta needed: 100g
	// Sauce: 1 jar available. Recipe needs 2 jars per 2 servings (scale=1.0).
	//   Monday: need 2, have 1 → shortfall 1, simulated → 0
	//   Wednesday: need 2, have 0 → shortfall 2, simulated → 0
	//   Total sauce needed: 3 jars

	var pastaNeeded, sauceNeeded float64
	for _, item := range weekItems {
		m := item.(map[string]any)
		switch m["name"] {
		case "Pasta":
			pastaNeeded += m["quantity_needed"].(float64)
		case "Tomato Sauce":
			sauceNeeded += m["quantity_needed"].(float64)
		}
	}

	if pastaNeeded < 100.0 {
		t.Errorf("weekly shopping: want Pasta quantity_needed >= 100g, got %v", pastaNeeded)
	}
	if sauceNeeded < 3.0 {
		t.Errorf("weekly shopping: want Tomato Sauce quantity_needed >= 3 jars, got %v", sauceNeeded)
	}
}

func TestWeeklyShoppingAccountsForInventory(t *testing.T) {
	mux, _ := newMux(t)

	// Add inventory: 2 eggs
	invW := httptest.NewRecorder()
	mux.ServeHTTP(invW, httptest.NewRequest("POST", "/api/inventory/",
		bytes.NewBufferString(`{"name":"CalEgg","quantity":2,"unit":"piece","location":"fridge"}`)))
	var inv map[string]any
	json.NewDecoder(invW.Body).Decode(&inv)
	invID := int(inv["id"].(float64))

	// Create recipe: needs 4 eggs per serving
	recW := httptest.NewRecorder()
	body := `{"name":"EggDish","servings":1,"ingredients":[{"name":"CalEgg","quantity":4,"unit":"piece","inventory_id":` + strconv.Itoa(invID) + `}]}`
	mux.ServeHTTP(recW, httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(body)))
	var rec map[string]any
	json.NewDecoder(recW.Body).Decode(&rec)
	recipeID := int(rec["id"].(float64))

	// Add recipe on Monday 2026-04-14 and Wednesday 2026-04-16
	for _, date := range []string{"2026-04-14", "2026-04-16"} {
		entry := `{"date":"` + date + `","meal_slot":"dinner","recipe_id":` + strconv.Itoa(recipeID) + `,"servings":1}`
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/calendar/", bytes.NewBufferString(entry)))
	}

	req := httptest.NewRequest("POST", "/api/calendar/generate-weekly-shopping?start=2026-04-14", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	items := result["items"].([]any)

	// Mon: need 4, have 2 → buy 2, stock=0
	// Wed: need 4, have 0 → buy 4
	// Total = 6
	total := 0.0
	for _, item := range items {
		m := item.(map[string]any)
		if m["name"] == "CalEgg" {
			total += m["quantity_needed"].(float64)
		}
	}
	if total != 6.0 {
		t.Errorf("want 6 eggs needed, got %v", total)
	}
}

func TestCreateInventoryItemInvalidUnit(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Pasta","quantity":500,"unit":"handful","location":"Pantry"}`
	req := httptest.NewRequest(http.MethodPost, "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateInventoryItemMismatchedPreferredUnit(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Pasta","quantity":500,"unit":"g","preferred_unit":"ml","location":"Pantry"}`
	req := httptest.NewRequest(http.MethodPost, "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestGetUnitsEndpoint(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest(http.MethodGet, "/api/units", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result map[string][]string
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result["mass"]) == 0 {
		t.Error("expected mass units")
	}
	if len(result["volume"]) == 0 {
		t.Error("expected volume units")
	}
	if len(result["count"]) == 0 {
		t.Error("expected count units")
	}
}

func TestCreateRecipeInvalidIngredientUnit(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Test Recipe","servings":2,"ingredients":[{"name":"Flour","quantity":500,"unit":"handful"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/recipes/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestRecipeAvailabilityWithUnitConversion(t *testing.T) {
	mux, db := newMux(t)

	// Add 1kg of flour (stored in kg)
	res, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,location,low_threshold,preferred_unit) VALUES ('Flour',1,'kg','Pantry',0.1,'kg')`)
	if err != nil {
		t.Fatal(err)
	}
	flourID, _ := res.LastInsertId()

	// Create recipe calling for 800g of flour (stored in g, but pantry has 1kg = 1000g)
	body := `{"name":"Bread","servings":1,"ingredients":[{"name":"Flour","quantity":800,"unit":"g","inventory_id":` + strconv.FormatInt(flourID, 10) + `}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/recipes/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create recipe failed: %d %s", w.Code, w.Body.String())
	}

	// Recipe should be available (1kg >= 800g)
	req2 := httptest.NewRequest(http.MethodGet, "/api/recipes/?available_only=true", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	var recipes []map[string]any
	json.Unmarshal(w2.Body.Bytes(), &recipes)
	if len(recipes) != 1 {
		t.Errorf("expected 1 available recipe, got %d", len(recipes))
	}
}

func TestCreateShoppingItemInvalidUnit(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Milk","quantity_needed":2,"unit":"gallon"}`
	req := httptest.NewRequest(http.MethodPost, "/api/shopping/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestWeeklyShoppingUnitConversion(t *testing.T) {
	mux, db := newMux(t)

	// Add 1kg of pasta, preferred_unit = kg
	res, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,low_threshold,preferred_unit) VALUES ('Pasta',1,'kg',0.1,'kg')`)
	if err != nil {
		t.Fatal(err)
	}
	pastaID, _ := res.LastInsertId()

	// Recipe uses 600g pasta per 1 serving
	res2, err := db.Exec(`INSERT INTO recipes (name,servings) VALUES ('Pasta Dish',1)`)
	if err != nil {
		t.Fatal(err)
	}
	recipeID, _ := res2.LastInsertId()
	_, err = db.Exec(`INSERT INTO recipe_ingredients (recipe_id,inventory_id,name,quantity,unit) VALUES (?,?,'Pasta',600,'g')`, recipeID, pastaID)
	if err != nil {
		t.Fatal(err)
	}

	// Add to calendar: Monday + Wednesday, 1 serving each
	_, err = db.Exec(`INSERT INTO meal_calendar (date,meal_slot,recipe_id,servings) VALUES ('2026-05-04','dinner',?,1)`, recipeID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO meal_calendar (date,meal_slot,recipe_id,servings) VALUES ('2026-05-06','dinner',?,1)`, recipeID)
	if err != nil {
		t.Fatal(err)
	}

	// Generate weekly shopping
	body := `{"week_start":"2026-05-04"}`
	req := httptest.NewRequest(http.MethodPost, "/api/calendar/generate-weekly-shopping", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Math:
	// 1kg on hand. Mon: needs 600g = 0.6kg, have 1kg, no shortfall. Simulated → 0.4kg.
	// Wed: needs 600g = 0.6kg, have 0.4kg, shortfall = 0.2kg.
	// Expect shopping list item for Pasta with quantity_needed ~0.2 and unit "kg"
	shoppingReq := httptest.NewRequest(http.MethodGet, "/api/shopping/", nil)
	shoppingW := httptest.NewRecorder()
	mux.ServeHTTP(shoppingW, shoppingReq)
	var items []map[string]any
	json.Unmarshal(shoppingW.Body.Bytes(), &items)

	found := false
	for _, item := range items {
		if item["name"] == "Pasta" {
			found = true
			if item["unit"] != "kg" {
				t.Errorf("expected unit kg, got %v", item["unit"])
			}
			qty, _ := item["quantity_needed"].(float64)
			if qty < 0.19 || qty > 0.21 {
				t.Errorf("expected quantity_needed ~0.2kg, got %v", qty)
			}
		}
	}
	if !found {
		t.Error("expected Pasta in shopping list")
	}
}

func TestInventorySuggestions(t *testing.T) {
	mux, db := newMux(t)

	// Insert two items
	_, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,preferred_unit,location,low_threshold,expiration_date,barcode) VALUES ('Pasta',500,'g','kg','Pantry',100,'','')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO inventory (name,quantity,unit,preferred_unit,location,low_threshold,expiration_date,barcode) VALUES ('Pasta Sauce',3,'jar','','Fridge',2,'','')`)
	if err != nil {
		t.Fatal(err)
	}

	// prefix "past" should return both
	req := httptest.NewRequest(http.MethodGet, "/api/inventory/suggestions?q=past", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var results []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for q=past, got %d", len(results))
	}
	// verify fields present on first result
	found := false
	for _, r := range results {
		if r["name"] == "Pasta" {
			found = true
			if r["unit"] != "g" {
				t.Errorf("expected unit g, got %v", r["unit"])
			}
			if r["location"] != "Pantry" {
				t.Errorf("expected location Pantry, got %v", r["location"])
			}
		}
	}
	if !found {
		t.Error("Pasta not found in suggestions")
	}

	// prefix "pasta s" should return only Pasta Sauce
	req2 := httptest.NewRequest(http.MethodGet, "/api/inventory/suggestions?q=pasta+s", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	var results2 []map[string]any
	json.Unmarshal(w2.Body.Bytes(), &results2)
	if len(results2) != 1 {
		t.Errorf("expected 1 result for q=pasta s, got %d", len(results2))
	}
	if results2[0]["name"] != "Pasta Sauce" {
		t.Errorf("expected Pasta Sauce, got %v", results2[0]["name"])
	}

	// no q → all items
	req3 := httptest.NewRequest(http.MethodGet, "/api/inventory/suggestions", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	var results3 []map[string]any
	json.Unmarshal(w3.Body.Bytes(), &results3)
	if len(results3) != 2 {
		t.Errorf("expected 2 results for no q, got %d", len(results3))
	}
}
