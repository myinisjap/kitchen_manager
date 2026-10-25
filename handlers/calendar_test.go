package handlers_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/testutil"
)

func newCalMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterCalendar(mux, db)
	handlers.RegisterMeals(mux, db)
	handlers.RegisterRecipes(mux, db)
	return mux, db
}

func addEntry(t *testing.T, db *sql.DB, date, slot string, rid int64, servings int) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO meal_calendar (date,meal_slot,recipe_id,servings) VALUES (?,?,?,?) RETURNING id`, date, slot, rid, servings).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

type calEntry struct {
	ID            int64  `json:"id"`
	Date          string `json:"date"`
	MealSlot      string `json:"meal_slot"`
	Servings      int    `json:"servings"`
	Cooked        bool   `json:"cooked"`
	MealHistoryID *int64 `json:"meal_history_id"`
}

func weekEntries(t *testing.T, mux *http.ServeMux, start string) map[int64]calEntry {
	t.Helper()
	w := call(t, mux, "GET", "/api/calendar/week?start="+start, "")
	if w.Code != 200 {
		t.Fatalf("week %d", w.Code)
	}
	var list []calEntry
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	m := map[int64]calEntry{}
	for _, e := range list {
		m[e.ID] = e
	}
	return m
}

func TestPatchCalendarEntry(t *testing.T) {
	mux, db := newCalMux(t)
	rid := newRecipe(t, db, "soup", 2)
	id := addEntry(t, db, "2024-01-01", "dinner", rid, 2)
	path := fmt.Sprintf("/api/calendar/%d", id)

	w := call(t, mux, "PATCH", path, `{"date":"2024-01-03","meal_slot":"lunch","servings":5}`)
	if w.Code != 200 {
		t.Fatalf("code %d %s", w.Code, w.Body)
	}
	var e calEntry
	json.Unmarshal(w.Body.Bytes(), &e)
	if e.Date != "2024-01-03" || e.MealSlot != "lunch" || e.Servings != 5 || e.Cooked {
		t.Fatalf("resp %+v", e)
	}
	var d, s string
	var sv int
	db.QueryRow(`SELECT date,meal_slot,servings FROM meal_calendar WHERE id=?`, id).Scan(&d, &s, &sv)
	if d != "2024-01-03" || s != "lunch" || sv != 5 {
		t.Fatalf("db %s %s %d", d, s, sv)
	}

	// partial
	if w := call(t, mux, "PATCH", path, `{"servings":3}`); w.Code != 200 {
		t.Fatalf("partial %d", w.Code)
	}
	db.QueryRow(`SELECT date,meal_slot,servings FROM meal_calendar WHERE id=?`, id).Scan(&d, &s, &sv)
	if d != "2024-01-03" || s != "lunch" || sv != 3 {
		t.Fatalf("partial db %s %s %d", d, s, sv)
	}

	// normalisation
	if w := call(t, mux, "PATCH", path, `{"meal_slot":" Snack "}`); w.Code != 200 {
		t.Fatalf("norm %d", w.Code)
	}
	db.QueryRow(`SELECT meal_slot FROM meal_calendar WHERE id=?`, id).Scan(&s)
	if s != "snack" {
		t.Fatalf("slot %q", s)
	}
}

func TestPatchCalendarErrors(t *testing.T) {
	mux, db := newCalMux(t)
	rid := newRecipe(t, db, "soup", 2)
	id := addEntry(t, db, "2024-01-01", "dinner", rid, 2)
	path := fmt.Sprintf("/api/calendar/%d", id)
	for name, c := range map[string]struct {
		path, body string
		want       int
	}{
		"slot":     {path, `{"meal_slot":"brunch"}`, 400},
		"date":     {path, `{"date":"01/02/2024"}`, 400},
		"servings": {path, `{"servings":0}`, 400},
		"type":     {path, `{"servings":"two"}`, 400},
		"empty":    {path, `{}`, 400},
		"missing":  {"/api/calendar/9999", `{"servings":2}`, 404},
		"badid":    {"/api/calendar/abc", `{"servings":2}`, 400},
	} {
		if w := call(t, mux, "PATCH", c.path, c.body); w.Code != c.want {
			t.Errorf("%s: got %d want %d", name, w.Code, c.want)
		}
	}
	var s string
	db.QueryRow(`SELECT meal_slot FROM meal_calendar WHERE id=?`, id).Scan(&s)
	if s != "dinner" {
		t.Fatalf("row changed: %s", s)
	}
}

func TestPostCalendarSlotValidation(t *testing.T) {
	mux, db := newCalMux(t)
	rid := newRecipe(t, db, "soup", 2)
	body := func(slot string) string {
		return fmt.Sprintf(`{"date":"2024-01-01","meal_slot":%q,"recipe_id":%d}`, slot, rid)
	}
	if w := call(t, mux, "POST", "/api/calendar/", body("brunch")); w.Code != 400 {
		t.Fatalf("brunch %d", w.Code)
	}
	if w := call(t, mux, "POST", "/api/calendar/", body("snack")); w.Code != 201 {
		t.Fatalf("snack %d", w.Code)
	}
	if w := call(t, mux, "POST", "/api/calendar/", body("")); w.Code != 201 {
		t.Fatalf("default %d", w.Code)
	}
}

func TestCookFromCalendarEntry(t *testing.T) {
	mux, db := newCalMux(t)
	inv := insertLot(t, db, "flour", 1000, "g", "", "")
	rid := recipeWith(t, db, inv, "flour", 100, "g") // recipe servings 1
	eid := addEntry(t, db, "2024-01-01", "dinner", rid, 3)

	w := call(t, mux, "POST", "/api/meals", fmt.Sprintf(`{"calendar_entry_id":%d}`, eid))
	if w.Code != 201 {
		t.Fatalf("cook %d %s", w.Code, w.Body)
	}
	var link sql.NullInt64
	var made int
	var mhid int64
	db.QueryRow(`SELECT id,calendar_entry_id,servings_made FROM meal_history`).Scan(&mhid, &link, &made)
	if !link.Valid || link.Int64 != eid || made != 3 {
		t.Fatalf("link %v made %d", link, made)
	}
	if q := qtyOf(t, db, inv); !near(q, 700) {
		t.Fatalf("stock %v", q)
	}
	e := weekEntries(t, mux, "2024-01-01")[eid]
	if !e.Cooked || e.MealHistoryID == nil || *e.MealHistoryID != mhid {
		t.Fatalf("entry %+v", e)
	}

	// second cook → 409, nothing changes
	if w := call(t, mux, "POST", "/api/meals", fmt.Sprintf(`{"recipe_id":%d,"calendar_entry_id":%d}`, rid, eid)); w.Code != 409 {
		t.Fatalf("second %d", w.Code)
	}
	if n := archCountRows(t, db, `SELECT COUNT(*) FROM meal_history`); n != 1 {
		t.Fatalf("history %d", n)
	}
	if q := qtyOf(t, db, inv); !near(q, 700) {
		t.Fatalf("stock after 409 %v", q)
	}

	// mismatch / unknown
	other := newRecipe(t, db, "other", 1)
	e2 := addEntry(t, db, "2024-01-02", "lunch", rid, 1)
	if w := call(t, mux, "POST", "/api/meals", fmt.Sprintf(`{"recipe_id":%d,"calendar_entry_id":%d}`, other, e2)); w.Code != 400 {
		t.Fatalf("mismatch %d", w.Code)
	}
	if w := call(t, mux, "POST", "/api/meals", `{"recipe_id":1,"calendar_entry_id":9999}`); w.Code != 404 {
		t.Fatalf("unknown %d", w.Code)
	}
}

func TestWeeklyShoppingSkipsCookedEntry(t *testing.T) {
	mux, db := newCalMux(t)
	inv := insertLot(t, db, "flour", 0, "g", "", "")
	rid := recipeWith(t, db, inv, "flour", 100, "g")
	e1 := addEntry(t, db, "2024-01-01", "dinner", rid, 1)
	addEntry(t, db, "2024-01-02", "dinner", rid, 1)
	if w := call(t, mux, "POST", "/api/meals", fmt.Sprintf(`{"calendar_entry_id":%d}`, e1)); w.Code != 201 {
		t.Fatalf("cook %d", w.Code)
	}
	w := call(t, mux, "POST", "/api/calendar/generate-weekly-shopping?start=2024-01-01", "")
	if w.Code != 200 {
		t.Fatalf("gen %d", w.Code)
	}
	var out struct {
		Items []struct {
			Name           string  `json:"name"`
			QuantityNeeded float64 `json:"quantity_needed"`
		} `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Items) != 1 || !near(out.Items[0].QuantityNeeded, 100) {
		t.Fatalf("items %+v", out.Items)
	}
}

func TestDeleteCookedEntryKeepsHistory(t *testing.T) {
	mux, db := newCalMux(t)
	rid := newRecipe(t, db, "soup", 1)
	eid := addEntry(t, db, "2024-01-01", "dinner", rid, 1)
	if w := call(t, mux, "POST", "/api/meals", fmt.Sprintf(`{"calendar_entry_id":%d}`, eid)); w.Code != 201 {
		t.Fatalf("cook %d", w.Code)
	}
	if w := call(t, mux, "DELETE", fmt.Sprintf("/api/calendar/%d", eid), ""); w.Code != 204 {
		t.Fatalf("delete %d", w.Code)
	}
	if n := archCountRows(t, db, `SELECT COUNT(*) FROM meal_history WHERE calendar_entry_id IS NULL`); n != 1 {
		t.Fatalf("history %d", n)
	}
}

func TestHardDeleteRecipeClearsCalendarLink(t *testing.T) {
	mux, db := newCalMux(t)
	rid := newRecipe(t, db, "soup", 1)
	eid := addEntry(t, db, "2024-01-01", "dinner", rid, 1)
	if w := call(t, mux, "POST", "/api/meals", fmt.Sprintf(`{"calendar_entry_id":%d}`, eid)); w.Code != 201 {
		t.Fatalf("cook %d", w.Code)
	}
	if w := call(t, mux, "DELETE", fmt.Sprintf("/api/recipes/%d", rid), ""); w.Code != 204 {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	if n := archCountRows(t, db, `SELECT COUNT(*) FROM meal_history WHERE calendar_entry_id IS NULL`); n != 1 {
		t.Fatalf("history %d", n)
	}
}
