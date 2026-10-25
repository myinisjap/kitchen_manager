package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/testutil"
)

func TestUseItUpFollowsExpirySetting(t *testing.T) {
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterSettings(mux, db)

	insertLot(t, db, "Milk", 1, "l", time.Now().AddDate(0, 0, 20).Format("2006-01-02"), "")
	var rid int64
	db.QueryRow(`INSERT INTO recipes (name) VALUES ('pancakes') RETURNING id`).Scan(&rid)
	db.Exec(`INSERT INTO recipe_ingredients (recipe_id, name, quantity) VALUES (?, 'milk', 1)`, rid)

	count := func() (int, int) {
		w := call(t, mux, "GET", "/api/recipes/use-it-up", "")
		if w.Code != 200 {
			t.Fatalf("code %d", w.Code)
		}
		var resp struct {
			Recipes []map[string]any `json:"recipes"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		w2 := call(t, mux, "GET", "/api/inventory/expiring", "")
		var items []map[string]any
		json.Unmarshal(w2.Body.Bytes(), &items)
		return len(resp.Recipes), len(items)
	}
	if r, i := count(); r != 0 || i != 0 {
		t.Fatalf("default: %d %d", r, i)
	}
	if w := call(t, mux, "PATCH", "/api/settings", `{"expiry_window_days":"30"}`); w.Code != 200 {
		t.Fatalf("patch %d", w.Code)
	}
	if r, i := count(); r != 1 || i != 1 {
		t.Fatalf("30d: %d %d", r, i)
	}
	for _, v := range []string{"0", "-1", "abc", "1000"} {
		if w := call(t, mux, "PATCH", "/api/settings", `{"expiry_window_days":"`+v+`"}`); w.Code != 400 {
			t.Fatalf("%s: %d", v, w.Code)
		}
	}
}
