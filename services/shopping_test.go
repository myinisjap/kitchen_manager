package services_test

import (
	"database/sql"
	"testing"

	"kitchen_manager/internal/testutil"
	"kitchen_manager/services"
)

func insertInv(t *testing.T, db *sql.DB, name string, qty float64, unit, prefUnit string, threshold float64) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`INSERT INTO inventory (name, quantity, unit, preferred_unit, low_threshold) VALUES (?,?,?,?,?) RETURNING id`,
		name, qty, unit, prefUnit, threshold).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func countShopping(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM shopping_list`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestGenerateFromThresholds(t *testing.T) {
	db := testutil.NewDB(t)
	n, err := services.GenerateFromThresholds(db)
	if err != nil || n != 0 {
		t.Fatalf("empty: n=%d err=%v", n, err)
	}

	low := insertInv(t, db, "flour", 200, "g", "", 500)
	insertInv(t, db, "equal", 5, "g", "", 5)
	insertInv(t, db, "plenty", 10, "g", "", 5)

	n, err = services.GenerateFromThresholds(db)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var invID int64
	var need float64
	var unit, source string
	if err := db.QueryRow(`SELECT inventory_id, quantity_needed, unit, source FROM shopping_list`).Scan(&invID, &need, &unit, &source); err != nil {
		t.Fatal(err)
	}
	if invID != low || need != 300 || unit != "g" || source != "threshold" {
		t.Errorf("got %d %v %s %s", invID, need, unit, source)
	}

	// idempotent
	n, err = services.GenerateFromThresholds(db)
	if err != nil || n != 0 || countShopping(t, db) != 1 {
		t.Errorf("second call n=%d err=%v rows=%d", n, err, countShopping(t, db))
	}

	// checked row does not block a new one
	if _, err := db.Exec(`UPDATE shopping_list SET checked=1`); err != nil {
		t.Fatal(err)
	}
	n, err = services.GenerateFromThresholds(db)
	if err != nil || n != 1 || countShopping(t, db) != 2 {
		t.Errorf("after check n=%d err=%v rows=%d", n, err, countShopping(t, db))
	}
}
