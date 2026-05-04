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
		preferred_unit TEXT NOT NULL DEFAULT '',
		unit_cost_cents INTEGER NOT NULL DEFAULT 0,
		quantity_per_scan REAL NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS inventory_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id INTEGER NOT NULL,
		item_name TEXT NOT NULL DEFAULT '',
		changed_at TEXT NOT NULL,
		changed_by TEXT NOT NULL DEFAULT 'system',
		change_type TEXT NOT NULL,
		quantity_before REAL,
		quantity_after REAL,
		unit TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		recipe_id INTEGER REFERENCES recipes(id)
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
	);
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS meal_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		recipe_id INTEGER NOT NULL REFERENCES recipes(id),
		recipe_name TEXT NOT NULL DEFAULT '',
		cooked_at TEXT NOT NULL,
		servings_made INTEGER NOT NULL DEFAULT 1,
		total_cost_cents INTEGER,
		notes TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS meal_history_ingredients (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		meal_history_id INTEGER NOT NULL REFERENCES meal_history(id),
		inventory_id INTEGER REFERENCES inventory(id),
		ingredient_name TEXT NOT NULL DEFAULT '',
		quantity_used REAL NOT NULL DEFAULT 0,
		unit TEXT NOT NULL DEFAULT '',
		cost_cents INTEGER
	);
	CREATE TABLE IF NOT EXISTS inventory_skus (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id      INTEGER NOT NULL REFERENCES inventory(id) ON DELETE CASCADE,
		barcode           TEXT    NOT NULL UNIQUE,
		quantity_per_scan REAL    NOT NULL DEFAULT 1
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
	if item["name"] != "milk" {
		t.Errorf("want name milk, got %v", item["name"])
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
		if item["name"] == "eggs" {
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
		if item["name"] == "yogurt" {
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
		if item["name"] == "thresholdtest" {
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
		if item["name"] == "recipeegg" {
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
		if item["name"] == "tomato sauce" {
			foundSauce = true
		}
		if item["name"] == "pasta" {
			foundPasta = true
		}
	}
	if !foundSauce {
		t.Error("threshold generation: tomato sauce should be in shopping list (below threshold)")
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
	// Pasta: 500g available. Recipe needs 300g × 2 occurrences = 600g total.
	//   600g needed - 500g available = 100g shortfall
	// Sauce: 1 can available. Recipe needs 2 cans × 2 occurrences = 4 cans total.
	//   4 needed - 1 available = 3 cans shortfall

	var pastaNeeded, sauceNeeded float64
	for _, item := range weekItems {
		m := item.(map[string]any)
		switch m["name"] {
		case "pasta":
			pastaNeeded += m["quantity_needed"].(float64)
		case "tomato sauce":
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

	// 2 recipes × 4 eggs = 8 total needed, 2 in inventory → shortfall = 6
	total := 0.0
	for _, item := range items {
		m := item.(map[string]any)
		if m["name"] == "calegg" {
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
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for q=pasta+s, got %d: %s", w2.Code, w2.Body.String())
	}
	var results2 []map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &results2); err != nil {
		t.Fatalf("invalid JSON for q=pasta+s: %v", err)
	}
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
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 for no-q, got %d: %s", w3.Code, w3.Body.String())
	}
	var results3 []map[string]any
	if err := json.Unmarshal(w3.Body.Bytes(), &results3); err != nil {
		t.Fatalf("invalid JSON for no-q: %v", err)
	}
	if len(results3) != 2 {
		t.Errorf("expected 2 results for no q, got %d", len(results3))
	}

	// q with no matches → empty array (not null)
	req4 := httptest.NewRequest(http.MethodGet, "/api/inventory/suggestions?q=xyz", nil)
	w4 := httptest.NewRecorder()
	mux.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Fatalf("expected 200 for no-match q, got %d: %s", w4.Code, w4.Body.String())
	}
	var results4 []map[string]any
	if err := json.Unmarshal(w4.Body.Bytes(), &results4); err != nil {
		t.Fatalf("invalid JSON for no-match: %v", err)
	}
	if len(results4) != 0 {
		t.Errorf("expected 0 results for q=xyz, got %d", len(results4))
	}
}

func TestGetInventoryItemByBarcode(t *testing.T) {
	mux, db := newMux(t)

	// Insert item with a known barcode
	_, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,preferred_unit,location,low_threshold,expiration_date,barcode) VALUES ('Olive Oil',1,'L','','Pantry',1,'','5000157024671')`)
	if err != nil {
		t.Fatal(err)
	}

	// Found: exact barcode match → 200 + correct item
	req := httptest.NewRequest(http.MethodGet, "/api/inventory/barcode/5000157024671", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if item["name"] != "Olive Oil" {
		t.Errorf("expected name Olive Oil, got %v", item["name"])
	}
	if item["barcode"] != "5000157024671" {
		t.Errorf("expected barcode 5000157024671, got %v", item["barcode"])
	}

	// Not found: unknown barcode → 404
	req2 := httptest.NewRequest(http.MethodGet, "/api/inventory/barcode/9999999999999", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w2.Code, w2.Body.String())
	}

	// Second item with different barcode should not be returned
	_, err = db.Exec(`INSERT INTO inventory (name,quantity,unit,preferred_unit,location,low_threshold,expiration_date,barcode) VALUES ('Butter',250,'g','','Fridge',50,'','1234567890123')`)
	if err != nil {
		t.Fatal(err)
	}
	req3 := httptest.NewRequest(http.MethodGet, "/api/inventory/barcode/1234567890123", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 for second barcode, got %d: %s", w3.Code, w3.Body.String())
	}
	var item3 map[string]any
	if err := json.Unmarshal(w3.Body.Bytes(), &item3); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if item3["name"] != "Butter" {
		t.Errorf("expected Butter, got %v", item3["name"])
	}
}

func TestDuplicateMergeOnPost(t *testing.T) {
	db := newTestDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/inventory/", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	// First insert — should create a new item.
	w1 := post(`{"name":"whole milk","quantity":2,"unit":"piece","barcode":"1234567890","expiration_date":"2026-06-01"}`)
	if w1.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w1.Code, w1.Body.String())
	}
	var r1 map[string]any
	json.Unmarshal(w1.Body.Bytes(), &r1)
	id1 := r1["id"]

	// Second post with same barcode/name/unit/expiration — should merge, not insert.
	w2 := post(`{"name":"whole milk","quantity":3,"unit":"piece","barcode":"1234567890","expiration_date":"2026-06-01"}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 (merge), got %d: %s", w2.Code, w2.Body.String())
	}
	var r2 map[string]any
	json.Unmarshal(w2.Body.Bytes(), &r2)
	if r2["id"] != id1 {
		t.Errorf("expected same id %v after merge, got %v", id1, r2["id"])
	}
	if r2["quantity"] != 5.0 {
		t.Errorf("expected merged quantity 5, got %v", r2["quantity"])
	}

	// Verify only one row exists.
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE name='whole milk'`).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 inventory row after merge, got %d", count)
	}

	// Post without barcode — should always insert a new row, never merge.
	w3 := post(`{"name":"whole milk","quantity":1,"unit":"piece","barcode":"","expiration_date":"2026-06-01"}`)
	if w3.Code != http.StatusCreated {
		t.Fatalf("expected 201 for no-barcode post, got %d: %s", w3.Code, w3.Body.String())
	}
	db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE name='whole milk'`).Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 rows after no-barcode insert, got %d", count)
	}

	// Post with same barcode but different expiration — should insert a new row.
	w4 := post(`{"name":"whole milk","quantity":1,"unit":"piece","barcode":"1234567890","expiration_date":"2026-07-01"}`)
	if w4.Code != http.StatusCreated {
		t.Fatalf("expected 201 for different expiry, got %d: %s", w4.Code, w4.Body.String())
	}
	db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE name='whole milk'`).Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 rows after different-expiry insert, got %d", count)
	}
}

func TestExpiryFirstDeductionCascade(t *testing.T) {
	db := newTestDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)

	// Insert three sibling items with different expiration dates.
	_, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,barcode,expiration_date,low_threshold) VALUES ('chicken breast',5,'lb','','2026-05-01',1)`)
	if err != nil {
		t.Fatal(err)
	}
	var id2, id3 int64
	db.QueryRow(`INSERT INTO inventory (name,quantity,unit,barcode,expiration_date,low_threshold) VALUES ('chicken breast',3,'lb','','2026-06-01',1) RETURNING id`).Scan(&id2)
	db.QueryRow(`INSERT INTO inventory (name,quantity,unit,barcode,expiration_date,low_threshold) VALUES ('chicken breast',4,'lb','','',1) RETURNING id`).Scan(&id3)

	// Get the id of the earliest-expiring item.
	var id1 int64
	db.QueryRow(`SELECT id FROM inventory WHERE expiration_date='2026-05-01'`).Scan(&id1)

	// Patch id2 qty to 0 — cascade deducts from earliest expiry first (id1), then id2.
	patchURL := "/api/inventory/" + strconv.FormatInt(id2, 10)
	req := httptest.NewRequest(http.MethodPatch, patchURL, bytes.NewBufferString(`{"quantity":0}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Deduction of id2 (qty=3, newQty=0, deduct=3):
	// Cascade order: id1 (exp 2026-05-01), id2 (exp 2026-06-01), id3 (no exp).
	// id1 has 5 >= 3 → partial deduct: id1 becomes 5-3=2. id2 untouched by cascade... wait.
	// Actually cascade deducts from earliest first: id1 qty=5, deduct=3 → id1 becomes 2, done.
	// id2 and id3 unchanged.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var qty1, qty2, qty3 float64
	db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, id1).Scan(&qty1)
	db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, id2).Scan(&qty2)
	db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, id3).Scan(&qty3)

	if qty1 != 2 {
		t.Errorf("expected id1 qty=2 after cascade, got %v", qty1)
	}
	if qty2 != 3 {
		t.Errorf("expected id2 qty=3 (untouched), got %v", qty2)
	}
	if qty3 != 4 {
		t.Errorf("expected id3 qty=4 (untouched), got %v", qty3)
	}

	// Now deduct the remaining 2 from id1 — cascade: id1(2) fully consumed → deleted, id2 untouched.
	patchURL1 := "/api/inventory/" + strconv.FormatInt(id1, 10)
	req2 := httptest.NewRequest(http.MethodPatch, patchURL1, bytes.NewBufferString(`{"quantity":0}`))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	// deduct=2, cascade: id1(2) fully consumed → deleted. id2(3) >= 2 → id2 becomes 1.
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	// id1 should be deleted.
	var exists int
	db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE id=?`, id1).Scan(&exists)
	if exists != 0 {
		t.Errorf("expected id1 deleted after full deduction, got count=%d", exists)
	}
	// deduct=2, id1(2) fully consumed → deduct hits 0, id2 untouched.
	db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, id2).Scan(&qty2)
	if qty2 != 3 {
		t.Errorf("expected id2 qty=3 (untouched), got %v", qty2)
	}
	// id3 (no expiry) untouched.
	db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, id3).Scan(&qty3)
	if qty3 != 4 {
		t.Errorf("expected id3 qty=4, got %v", qty3)
	}
}

func TestBarcodeLookupFallsBackToSkuAlias(t *testing.T) {
	mux, db := newMux(t)
	// Create an inventory item with a primary barcode
	body := `{"name":"Flour","quantity":500,"unit":"g","barcode":"PRIMARY001","quantity_per_scan":500}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create item: want 201, got %d: %s", w.Code, w.Body)
	}
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int64(created["id"].(float64))

	// Insert a SKU alias manually
	_, err := db.Exec(`INSERT INTO inventory_skus (inventory_id, barcode, quantity_per_scan) VALUES (?,?,?)`, id, "ALIAS001", 250.0)
	if err != nil {
		t.Fatal(err)
	}

	// Lookup by alias barcode
	req2 := httptest.NewRequest("GET", "/api/inventory/barcode/ALIAS001", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("alias lookup: want 200, got %d: %s", w2.Code, w2.Body)
	}
	var item map[string]any
	json.NewDecoder(w2.Body).Decode(&item)
	if item["name"] != "flour" {
		t.Errorf("want name flour, got %v", item["name"])
	}
	if item["quantity_per_scan"].(float64) != 250.0 {
		t.Errorf("want quantity_per_scan 250, got %v", item["quantity_per_scan"])
	}
	if item["sku_id"] == nil {
		t.Error("want sku_id in response")
	}
}

func TestSimilarSearch(t *testing.T) {
	mux, _ := newMux(t)
	for _, b := range []string{
		`{"name":"Whole Milk","quantity":1,"unit":"L"}`,
		`{"name":"Almond Milk","quantity":1,"unit":"L"}`,
		`{"name":"Butter","quantity":200,"unit":"g"}`,
	} {
		req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(b))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}

	req := httptest.NewRequest("GET", "/api/inventory/similar?name=milk", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var results []map[string]any
	json.NewDecoder(w.Body).Decode(&results)
	if len(results) != 2 {
		t.Errorf("want 2 milk results, got %d", len(results))
	}

	// Empty name returns []
	req2 := httptest.NewRequest("GET", "/api/inventory/similar?name=", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	var empty []map[string]any
	json.NewDecoder(w2.Body).Decode(&empty)
	if len(empty) != 0 {
		t.Errorf("want empty results, got %d", len(empty))
	}
}

func TestCreateSkuAliasAndMergeQuantity(t *testing.T) {
	mux, db := newMux(t)
	body := `{"name":"Flour","quantity":500,"unit":"g","preferred_unit":"g","quantity_per_scan":500}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int64(created["id"].(float64))

	// POST SKU alias with quantity
	skuBody := `{"barcode":"FLOUR002","quantity_per_scan":250,"quantity":250,"unit":"g"}`
	req2 := httptest.NewRequest("POST", "/api/skus/item/"+strconv.FormatInt(id, 10), bytes.NewBufferString(skuBody))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w2.Code, w2.Body)
	}

	var qty float64
	db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, id).Scan(&qty)
	if qty != 750 {
		t.Errorf("want quantity 750, got %v", qty)
	}

	var skuCount int
	db.QueryRow(`SELECT COUNT(*) FROM inventory_skus WHERE inventory_id=? AND barcode='FLOUR002'`, id).Scan(&skuCount)
	if skuCount != 1 {
		t.Error("want 1 SKU alias row")
	}
}

func TestCreateSkuAliasWithUnitConversion(t *testing.T) {
	mux, db := newMux(t)
	body := `{"name":"Sugar","quantity":1,"unit":"kg","preferred_unit":"kg"}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int64(created["id"].(float64))

	// Add 500g — should add 0.5kg
	skuBody := `{"barcode":"SUGAR002","quantity_per_scan":500,"quantity":500,"unit":"g"}`
	req2 := httptest.NewRequest("POST", "/api/skus/item/"+strconv.FormatInt(id, 10), bytes.NewBufferString(skuBody))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w2.Code, w2.Body)
	}

	var qty float64
	db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, id).Scan(&qty)
	if qty < 1.499 || qty > 1.501 {
		t.Errorf("want quantity ~1.5kg, got %v", qty)
	}
}

func TestCreateSkuAliasDuplicateBarcode(t *testing.T) {
	mux, _ := newMux(t)
	// Create item with primary barcode
	body := `{"name":"Rice","quantity":1,"unit":"kg","barcode":"RICE001"}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int64(created["id"].(float64))

	// Try to add alias with same barcode as primary — expect 409
	skuBody := `{"barcode":"RICE001","quantity_per_scan":1,"quantity":0,"unit":"kg"}`
	req2 := httptest.NewRequest("POST", "/api/skus/item/"+strconv.FormatInt(id, 10), bytes.NewBufferString(skuBody))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Errorf("want 409 conflict, got %d", w2.Code)
	}
}

func TestListAndDeleteSkuAliases(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Oats","quantity":1,"unit":"kg"}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int64(created["id"].(float64))
	idStr := strconv.FormatInt(id, 10)

	// Add two aliases
	for _, bc := range []string{"OATS001", "OATS002"} {
		skuBody := `{"barcode":"` + bc + `","quantity_per_scan":1,"quantity":0,"unit":""}`
		req := httptest.NewRequest("POST", "/api/skus/item/"+idStr, bytes.NewBufferString(skuBody))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}

	// List
	req2 := httptest.NewRequest("GET", "/api/skus/item/"+idStr, nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	var skus []map[string]any
	json.NewDecoder(w2.Body).Decode(&skus)
	if len(skus) != 2 {
		t.Fatalf("want 2 skus, got %d", len(skus))
	}

	// Delete first
	skuID := int64(skus[0]["id"].(float64))
	req3 := httptest.NewRequest("DELETE", "/api/skus/"+strconv.FormatInt(skuID, 10), nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNoContent {
		t.Errorf("want 204, got %d", w3.Code)
	}

	// List again — should have 1
	req4 := httptest.NewRequest("GET", "/api/skus/item/"+idStr, nil)
	w4 := httptest.NewRecorder()
	mux.ServeHTTP(w4, req4)
	var skus2 []map[string]any
	json.NewDecoder(w4.Body).Decode(&skus2)
	if len(skus2) != 1 {
		t.Errorf("want 1 sku, got %d", len(skus2))
	}
}

func TestDeleteInventoryItemCascadesSkus(t *testing.T) {
	mux, db := newMux(t)
	body := `{"name":"Pasta","quantity":500,"unit":"g"}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int64(created["id"].(float64))
	idStr := strconv.FormatInt(id, 10)

	// Add alias
	skuBody := `{"barcode":"PASTA001","quantity_per_scan":500,"quantity":0,"unit":""}`
	req2 := httptest.NewRequest("POST", "/api/skus/item/"+idStr, bytes.NewBufferString(skuBody))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("create sku: want 201, got %d: %s", w2.Code, w2.Body)
	}

	// Delete inventory item
	req3 := httptest.NewRequest("DELETE", "/api/inventory/"+idStr, nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNoContent {
		t.Fatalf("delete item: want 204, got %d", w3.Code)
	}

	// SKU should be gone
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM inventory_skus WHERE inventory_id=?`, id).Scan(&count)
	if count != 0 {
		t.Errorf("expected cascade delete of SKU, got %d rows", count)
	}
}

func TestGetGroupedInventory(t *testing.T) {
	mux, db := newMux(t)

	// Insert two rows: same name+unit, different locations and expiration dates.
	// id1: Pantry, expires sooner; id2: Fridge, expires later.
	var id1, id2 int64
	err := db.QueryRow(`INSERT INTO inventory (name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit) VALUES ('olive oil',0.5,'L','Pantry','2026-06-01',1,'','') RETURNING id`).Scan(&id1)
	if err != nil {
		t.Fatal(err)
	}
	err = db.QueryRow(`INSERT INTO inventory (name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit) VALUES ('olive oil',1.0,'L','Fridge','2026-09-01',1,'','') RETURNING id`).Scan(&id2)
	if err != nil {
		t.Fatal(err)
	}

	getGrouped := func(query string) []map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/inventory/grouped"+query, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
		}
		var result []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		return result
	}

	// --- Test 1: Basic grouping ---
	// Two rows with same name+unit → one group with two location entries and correct total_quantity.
	groups := getGrouped("")
	if len(groups) != 1 {
		t.Fatalf("basic grouping: want 1 group, got %d", len(groups))
	}
	group := groups[0]
	if group["name"] != "olive oil" {
		t.Errorf("basic grouping: want name 'olive oil', got %v", group["name"])
	}
	if group["total_quantity"].(float64) != 1.5 {
		t.Errorf("basic grouping: want total_quantity 1.5, got %v", group["total_quantity"])
	}
	locs := group["locations"].([]any)
	if len(locs) != 2 {
		t.Fatalf("basic grouping: want 2 locations, got %d", len(locs))
	}

	// --- Test 2: Sort order — locations sorted by expiration ASC, empty expiration last ---
	// id1 expires 2026-06-01 (sooner), id2 expires 2026-09-01 (later).
	// First entry should be the Pantry one (soonest expiry).
	loc0 := locs[0].(map[string]any)
	if loc0["location"] != "Pantry" {
		t.Errorf("sort order: want first location 'Pantry' (soonest expiry), got %v", loc0["location"])
	}
	loc1 := locs[1].(map[string]any)
	if loc1["location"] != "Fridge" {
		t.Errorf("sort order: want second location 'Fridge', got %v", loc1["location"])
	}

	// --- Test 3: recommended_location_id — should be id of soonest-expiring row ---
	recID := int64(group["recommended_location_id"].(float64))
	if recID != id1 {
		t.Errorf("recommended_location_id: want id1 (%d, soonest expiry), got %d", id1, recID)
	}

	// Insert a row with no expiration date to verify it sorts last.
	var id3 int64
	err = db.QueryRow(`INSERT INTO inventory (name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit) VALUES ('olive oil',0.25,'L','Cellar','',1,'','') RETURNING id`).Scan(&id3)
	if err != nil {
		t.Fatal(err)
	}
	groups2 := getGrouped("")
	if len(groups2) != 1 {
		t.Fatalf("sort with no-expiry: want 1 group, got %d", len(groups2))
	}
	locs2 := groups2[0]["locations"].([]any)
	if len(locs2) != 3 {
		t.Fatalf("sort with no-expiry: want 3 locations, got %d", len(locs2))
	}
	// Last entry should be the one with empty expiration.
	lastLoc := locs2[2].(map[string]any)
	if lastLoc["location"] != "Cellar" {
		t.Errorf("sort order: want last location 'Cellar' (no expiry), got %v", lastLoc["location"])
	}

	// --- Test 4: Empty response for non-matching name filter → [] (not null/404) ---
	groups3 := getGrouped("?name=doesnotexist")
	if groups3 == nil {
		t.Error("empty name filter: want [] not nil")
	}
	if len(groups3) != 0 {
		t.Errorf("empty name filter: want 0 groups, got %d", len(groups3))
	}

	// --- Test 5: Location filter → only returns groups that have a row in that location ---
	groups4 := getGrouped("?location=Pantry")
	if len(groups4) != 1 {
		t.Fatalf("location filter: want 1 group for Pantry, got %d", len(groups4))
	}
	locs4 := groups4[0]["locations"].([]any)
	if len(locs4) != 1 {
		t.Fatalf("location filter: want 1 location entry for Pantry, got %d", len(locs4))
	}
	pantryLoc := locs4[0].(map[string]any)
	if pantryLoc["location"] != "Pantry" {
		t.Errorf("location filter: want location 'Pantry', got %v", pantryLoc["location"])
	}

	// Location filter for a location with no items → empty array.
	groups5 := getGrouped("?location=Freezer")
	if groups5 == nil {
		t.Error("location filter no match: want [] not nil")
	}
	if len(groups5) != 0 {
		t.Errorf("location filter no match: want 0 groups, got %d", len(groups5))
	}
}

func TestDeductWithLocationID(t *testing.T) {
	mux, _ := newMux(t)

	// Insert Fridge row: milk 5 oz
	fridgeBody := `{"name":"milk","quantity":5,"unit":"oz","location":"Fridge"}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(fridgeBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create fridge row: want 201, got %d: %s", w.Code, w.Body)
	}
	var fridgeItem map[string]any
	json.NewDecoder(w.Body).Decode(&fridgeItem)
	fridgeID := int64(fridgeItem["id"].(float64))

	// Insert Pantry row: milk 10 oz
	pantryBody := `{"name":"milk","quantity":10,"unit":"oz","location":"Pantry"}`
	req2 := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(pantryBody))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("create pantry row: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var pantryItem map[string]any
	json.NewDecoder(w2.Body).Decode(&pantryItem)
	pantryID := int64(pantryItem["id"].(float64))

	// PATCH the Fridge row with quantity=3 and location_id=fridgeID
	patchBody := `{"quantity":3,"location_id":` + strconv.FormatInt(fridgeID, 10) + `}`
	req3 := httptest.NewRequest("PATCH", "/api/inventory/"+strconv.FormatInt(fridgeID, 10), bytes.NewBufferString(patchBody))
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("patch with location_id: want 200, got %d: %s", w3.Code, w3.Body)
	}

	// Verify Fridge row quantity is now 2
	req4 := httptest.NewRequest("GET", "/api/inventory/"+strconv.FormatInt(fridgeID, 10), nil)
	w4 := httptest.NewRecorder()
	mux.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Fatalf("get fridge row: want 200, got %d", w4.Code)
	}
	var fridgeResult map[string]any
	json.NewDecoder(w4.Body).Decode(&fridgeResult)
	if fridgeResult["quantity"] != 3.0 {
		t.Errorf("fridge quantity: want 3, got %v", fridgeResult["quantity"])
	}

	// Verify Pantry row quantity is still 10 (no cascade)
	req5 := httptest.NewRequest("GET", "/api/inventory/"+strconv.FormatInt(pantryID, 10), nil)
	w5 := httptest.NewRecorder()
	mux.ServeHTTP(w5, req5)
	if w5.Code != http.StatusOK {
		t.Fatalf("get pantry row: want 200, got %d", w5.Code)
	}
	var pantryResult map[string]any
	json.NewDecoder(w5.Body).Decode(&pantryResult)
	if pantryResult["quantity"] != 10.0 {
		t.Errorf("pantry quantity: want 10 (no cascade), got %v", pantryResult["quantity"])
	}
}
