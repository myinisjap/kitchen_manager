package handlers_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"kitchen_manager/handlers"
)

func countRows(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func recipeIDs(t *testing.T, mux http.Handler, path string) map[int64]bool {
	t.Helper()
	w := call(t, mux.(*http.ServeMux), "GET", path, "")
	if w.Code != 200 {
		t.Fatalf("GET %s: %d", path, w.Code)
	}
	var list []struct {
		ID       int64 `json:"id"`
		Archived bool  `json:"archived"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	out := map[int64]bool{}
	for _, r := range list {
		out[r.ID] = r.Archived
	}
	return out
}

func TestArchiveRecipeKeepsHistory(t *testing.T) {
	mux, db := newRecipeMux(t)
	id := insertLot(t, db, "milk", 1000, "ml", "", "")
	rid := recipeWith(t, db, id, "milk", 100, "ml")
	if code := cook(t, mux, rid); code != http.StatusCreated {
		t.Fatalf("cook %d", code)
	}
	ingBefore := countRows(t, db, `SELECT COUNT(*) FROM meal_history_ingredients`)

	w := call(t, mux, "POST", fmt.Sprintf("/api/recipes/%d/archive", rid), "")
	if w.Code != 200 {
		t.Fatalf("archive %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["archived"] != true {
		t.Fatalf("body %v", body)
	}
	if _, ok := recipeIDs(t, mux, "/api/recipes/")[rid]; ok {
		t.Fatal("archived recipe in default list")
	}
	if a, ok := recipeIDs(t, mux, "/api/recipes/?include_archived=true")[rid]; !ok || !a {
		t.Fatal("archived recipe missing with include_archived")
	}
	if w := call(t, mux, "GET", fmt.Sprintf("/api/recipes/%d", rid), ""); w.Code != 200 {
		t.Fatalf("get %d", w.Code)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM meal_history WHERE recipe_id=?`, rid); n != 1 {
		t.Fatalf("history %d", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM meal_history_ingredients`); n != ingBefore {
		t.Fatalf("ingredients %d vs %d", n, ingBefore)
	}
	if w := call(t, mux, "POST", fmt.Sprintf("/api/recipes/%d/unarchive", rid), ""); w.Code != 200 {
		t.Fatalf("unarchive %d", w.Code)
	}
	if _, ok := recipeIDs(t, mux, "/api/recipes/")[rid]; !ok {
		t.Fatal("unarchived recipe missing")
	}
}

func TestArchiveMissingRecipe404(t *testing.T) {
	mux, _ := newRecipeMux(t)
	for _, a := range []string{"archive", "unarchive"} {
		if w := call(t, mux, "POST", "/api/recipes/999/"+a, ""); w.Code != 404 {
			t.Errorf("%s: %d", a, w.Code)
		}
	}
}

func TestDeleteRecipeKeepsMealHistory(t *testing.T) {
	mux, db := newRecipeMux(t)
	id := insertLot(t, db, "milk", 1000, "ml", "", "")
	rid := recipeWith(t, db, id, "milk", 100, "ml")
	if code := cook(t, mux, rid); code != http.StatusCreated {
		t.Fatalf("cook %d", code)
	}
	ingBefore := countRows(t, db, `SELECT COUNT(*) FROM meal_history_ingredients`)
	if ingBefore == 0 {
		t.Fatal("expected history ingredients")
	}
	if w := call(t, mux, "DELETE", fmt.Sprintf("/api/recipes/%d", rid), ""); w.Code != 204 {
		t.Fatalf("delete %d", w.Code)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM recipes WHERE id=?`, rid); n != 0 {
		t.Fatal("recipe still present")
	}
	var name string
	if err := db.QueryRow(`SELECT recipe_name FROM meal_history WHERE recipe_id IS NULL`).Scan(&name); err != nil || name != "r" {
		t.Fatalf("history row: %q %v", name, err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM meal_history_ingredients`); n != ingBefore {
		t.Fatalf("ingredients %d vs %d", n, ingBefore)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM inventory_history WHERE recipe_id IS NOT NULL`); n != 0 {
		t.Fatalf("inventory_history still linked: %d", n)
	}

	w := call(t, mux, "GET", "/api/meals", "")
	if w.Code != 200 {
		t.Fatalf("meals %d %s", w.Code, w.Body)
	}
	var list struct {
		Meals []map[string]any `json:"meals"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Meals) != 1 || list.Meals[0]["recipe_id"] != nil || list.Meals[0]["recipe_name"] != "r" {
		t.Fatalf("meals %s", w.Body)
	}

	w = call(t, mux, "GET", "/api/meals/stats", "")
	if w.Code != 200 {
		t.Fatalf("stats %d", w.Code)
	}
	var st struct {
		MostCooked []map[string]any `json:"most_cooked"`
	}
	json.Unmarshal(w.Body.Bytes(), &st)
	if len(st.MostCooked) != 1 || st.MostCooked[0]["recipe_id"] != nil {
		t.Fatalf("stats %s", w.Body)
	}
}

func newRecipeMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	mux, db := newMealsMux(t)
	handlers.RegisterRecipes(mux, db)
	return mux, db
}
