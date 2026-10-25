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

func shoppingRows(t *testing.T, db *sql.DB) (n int, qty float64, unit string, invID int64) {
	t.Helper()
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(quantity_needed),0), COALESCE(MAX(unit),''), COALESCE(MAX(inventory_id),0) FROM shopping_list`).Scan(&n, &qty, &unit, &invID); err != nil {
		t.Fatal(err)
	}
	return
}

func TestGenerateFromThresholds_MultiLotAbovePar(t *testing.T) {
	db := testutil.NewDB(t)
	insertInv(t, db, "milk", 0.5, "L", "", 1)
	insertInv(t, db, "milk", 3, "L", "", 1)
	added, err := services.GenerateFromThresholds(db)
	if err != nil || added != 0 {
		t.Fatalf("added=%d err=%v", added, err)
	}
}

func TestGenerateFromThresholds_MultiLotRestockToPar(t *testing.T) {
	db := testutil.NewDB(t)
	first := insertInv(t, db, "milk", 0.25, "L", "L", 2)
	second := insertInv(t, db, "milk", 250, "ml", "L", 2)
	added, err := services.GenerateFromThresholds(db)
	if err != nil || added != 1 {
		t.Fatalf("added=%d err=%v", added, err)
	}
	n, qty, unit, inv := shoppingRows(t, db)
	if n != 1 || qty != 1.5 || unit != "L" || inv != first {
		t.Fatalf("got n=%d qty=%v unit=%q inv=%d", n, qty, unit, inv)
	}
	if added, _ = services.GenerateFromThresholds(db); added != 0 {
		t.Fatalf("second call added %d", added)
	}
	// An unchecked row linked to any lot of the product blocks too.
	db.Exec(`DELETE FROM shopping_list`)
	db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,'manual')`, second, "milk", 1, "L")
	if added, _ = services.GenerateFromThresholds(db); added != 0 {
		t.Fatalf("linked-to-second-lot row did not block: %d", added)
	}
}

func TestGenerateFromThresholds_ParUnsetZero(t *testing.T) {
	db := testutil.NewDB(t)
	insertInv(t, db, "salt", 0, "g", "", 0)
	if added, err := services.GenerateFromThresholds(db); err != nil || added != 0 {
		t.Fatalf("added=%d err=%v", added, err)
	}
}

func TestGenerateFromThresholds_ParIsMaxOfLots(t *testing.T) {
	db := testutil.NewDB(t)
	insertInv(t, db, "rice", 0.5, "kg", "kg", 1)
	insertInv(t, db, "rice", 0.5, "kg", "kg", 4)
	if added, err := services.GenerateFromThresholds(db); err != nil || added != 1 {
		t.Fatalf("added=%d err=%v", added, err)
	}
	if _, qty, _, _ := shoppingRows(t, db); qty != 3 {
		t.Fatalf("qty=%v want 3", qty)
	}
}

func TestProductLevels_SkipsUnconvertibleLot(t *testing.T) {
	db := testutil.NewDB(t)
	insertInv(t, db, "egg", 100, "g", "", 0)
	insertInv(t, db, "egg", 5, "piece", "", 0)
	lv, err := services.ProductLevels(db)
	if err != nil || len(lv) != 1 {
		t.Fatalf("%v %v", lv, err)
	}
	if lv[0].Total != 100 || lv[0].Unit != "g" || len(lv[0].LotIDs) != 2 {
		t.Fatalf("%+v", lv[0])
	}
}
