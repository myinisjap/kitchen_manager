package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/testutil"
)

func TestCookScalesByServings(t *testing.T) {
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterMeals(mux, db)
	inv := insertLot(t, db, "Flour", 1000, "g", "", "")
	rid := newRecipe(t, db, "r", 2)
	if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id,product_id,name,quantity,unit) VALUES (?,(SELECT product_id FROM stock_lots WHERE id=?),?,?,?)`, rid, inv, "Flour", 100, "g"); err != nil {
		t.Fatal(err)
	}
	qty := func() float64 {
		var q float64
		db.QueryRow(`SELECT quantity FROM stock_lots WHERE id=?`, inv).Scan(&q)
		return q
	}
	if w := call(t, mux, "POST", "/api/meals", fmt.Sprintf(`{"recipe_id":%d,"servings_made":4}`, rid)); w.Code >= 300 {
		t.Fatal(w.Code, w.Body.String())
	}
	if q := qty(); q != 800 {
		t.Errorf("after 4 servings: %v", q)
	}
	if w := call(t, mux, "POST", "/api/meals", fmt.Sprintf(`{"recipe_id":%d,"servings_made":1}`, rid)); w.Code >= 300 {
		t.Fatal(w.Code, w.Body.String())
	}
	if q := qty(); q != 750 {
		t.Errorf("after 1 serving: %v", q)
	}
}

func TestAddToShoppingListScaling(t *testing.T) {
	for _, c := range []struct {
		query string
		have  float64
		want  float64
	}{{"?servings=2", 0, 200}, {"", 0, 400}, {"?servings=8", 100, 700}} {
		mux, db := newMux(t)
		rid := newRecipe(t, db, "r", 4)
		var inv any
		if c.have > 0 {
			inv = insertLot(t, db, "Rice", c.have, "g", "", "")
		}
		if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id,product_id,name,quantity,unit) VALUES (?,(SELECT product_id FROM stock_lots WHERE id=?),?,?,?)`, rid, inv, "Rice", 400, "g"); err != nil {
			t.Fatal(err)
		}
		w := call(t, mux, "POST", fmt.Sprintf("/api/recipes/%d/add-to-shopping-list%s", rid, c.query), "")
		if w.Code >= 300 {
			t.Fatal(c.query, w.Code, w.Body.String())
		}
		var got float64
		if err := db.QueryRow(`SELECT quantity_needed FROM shopping_list WHERE name='Rice'`).Scan(&got); err != nil {
			t.Fatal(c.query, err)
		}
		if got != c.want {
			t.Errorf("%q: got %v want %v", c.query, got, c.want)
		}
	}
}

func TestCalendarDefaultsToRecipeServings(t *testing.T) {
	mux, db := newMux(t)
	rid := newRecipe(t, db, "r", 4)
	for _, c := range []struct {
		body string
		want int
	}{{`"servings":3`, 3}, {`"servings":0`, 4}, {`"servings":-2`, 4}, {`"x":1`, 4}} {
		w := call(t, mux, "POST", "/api/calendar/", fmt.Sprintf(`{"date":"2026-01-05","recipe_id":%d,%s}`, rid, c.body))
		if w.Code != http.StatusCreated {
			t.Fatal(c.body, w.Code, w.Body.String())
		}
		var out struct{ Servings int }
		json.Unmarshal(w.Body.Bytes(), &out)
		if out.Servings != c.want {
			t.Errorf("%s: got %d want %d", c.body, out.Servings, c.want)
		}
	}
	if w := call(t, mux, "POST", "/api/calendar/", `{"date":"2026-01-05","recipe_id":9999}`); w.Code != http.StatusNotFound {
		t.Errorf("missing recipe: %d", w.Code)
	}
}
