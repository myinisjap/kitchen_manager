package services_test

import (
	"testing"
	"time"

	"kitchen_manager/internal/testutil"
	"kitchen_manager/services"
)

func day(n int) string { return time.Now().AddDate(0, 0, n).Format("2006-01-02") }

func TestRankUseItUp(t *testing.T) {
	db := testutil.NewDB(t)
	lot := func(name string, qty float64, exp string) int64 {
		pid, _ := testutil.AddLot(t, db, name, qty, "g", testutil.WithExpiry(exp))
		return pid
	}
	recipe := func(name string, archived int, ings ...any) int64 {
		var id int64
		if err := db.QueryRow(`INSERT INTO recipes (name, archived) VALUES (?,?) RETURNING id`, name, archived).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, ing := range ings {
			switch v := ing.(type) {
			case string:
				db.Exec(`INSERT INTO recipe_ingredients (recipe_id, name, quantity) VALUES (?,?,1)`, id, v)
			case int64:
				db.Exec(`INSERT INTO recipe_ingredients (recipe_id, product_id, name, quantity) VALUES (?,?,?,1)`, id, v, "linked")
			}
		}
		return id
	}
	cutoff := day(7)

	empty, err := services.RankUseItUp(db, cutoff, 5)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty: %v %v", empty, err)
	}

	lot("Milk", 1, day(3))
	lot("Milk", 2, day(-1)) // second lot, same product
	eggs := lot("Eggs", 1, day(5))
	lot("Flour", 1, day(30))
	lot("Butter", 0, day(1))
	lot("Rice", 1, "")
	lot("Cheese", 1, day(6))

	a := recipe("A", 0, "milk ", "MILK", "eggs")
	b := recipe("B", 0, "Milk")
	recipe("C", 0, "flour", "butter", "rice")
	recipe("Arch", 1, "milk", "eggs")
	d := recipe("D", 0, eggs) // via inventory_id, name differs
	e := recipe("E", 0, "cheese")

	got, err := services.RankUseItUp(db, cutoff, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{a, b, d, e}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, id := range want {
		if got[i].RecipeID != id {
			t.Fatalf("pos %d: got %+v want id %d", i, got, id)
		}
	}
	if got[0].ExpiringCount != 2 || got[1].ExpiringCount != 1 {
		t.Fatalf("counts: %+v", got)
	}
	if got[0].ExpiringItems[0] != "Milk" || got[0].EarliestExpiry != day(-1) {
		t.Fatalf("items: %+v", got[0])
	}
	lim, _ := services.RankUseItUp(db, cutoff, 2)
	if len(lim) != 2 {
		t.Fatalf("limit: %d", len(lim))
	}
}
