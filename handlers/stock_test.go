package handlers_test

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/database"
)

// newSchemaMux builds handlers on a database created by the real schema code,
// so foreign keys and migrations match production.
func newSchemaMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	db, err := database.OpenDB(filepath.Join(t.TempDir(), "kitchen.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	return mux, db
}

func call(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func insertLot(t *testing.T, db *sql.DB, name string, qty float64, unit, expiry, barcode string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO inventory (name,quantity,unit,expiration_date,barcode,low_threshold) VALUES (?,?,?,?,?,1) RETURNING id`,
		name, qty, unit, expiry, barcode).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func linkRecipe(t *testing.T, db *sql.DB, invID int64) int64 {
	t.Helper()
	var recipeID int64
	if err := db.QueryRow(`INSERT INTO recipes (name) VALUES ('bread') RETURNING id`).Scan(&recipeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id,inventory_id,name,quantity,unit) VALUES (?,?,'flour',100,'g')`, recipeID, invID); err != nil {
		t.Fatal(err)
	}
	return recipeID
}

func ingredientInventoryID(t *testing.T, db *sql.DB, recipeID int64) sql.NullInt64 {
	t.Helper()
	var invID sql.NullInt64
	if err := db.QueryRow(`SELECT inventory_id FROM recipe_ingredients WHERE recipe_id=?`, recipeID).Scan(&invID); err != nil {
		t.Fatal(err)
	}
	return invID
}

func TestDeductLinkedItemToZeroKeepsItem(t *testing.T) {
	mux, db := newSchemaMux(t)
	id := insertLot(t, db, "flour", 500, "g", "2026-12-01", "FLOUR1")
	recipeID := linkRecipe(t, db, id)

	w := call(t, mux, http.MethodPatch, "/api/inventory/"+strconv.FormatInt(id, 10), `{"quantity":0}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var qty float64
	var expiry string
	if err := db.QueryRow(`SELECT quantity, expiration_date FROM inventory WHERE id=?`, id).Scan(&qty, &expiry); err != nil {
		t.Fatalf("item should be kept at zero: %v", err)
	}
	if qty != 0 || expiry != "" {
		t.Errorf("expected qty 0 and cleared expiry, got qty=%v expiry=%q", qty, expiry)
	}
	if got := ingredientInventoryID(t, db, recipeID); !got.Valid || got.Int64 != id {
		t.Errorf("recipe link changed: %+v", got)
	}

	// Out-of-stock items must still reach the shopping list.
	w = call(t, mux, http.MethodPost, "/api/shopping/generate-from-thresholds", "")
	if w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	var onList int
	db.QueryRow(`SELECT COUNT(*) FROM shopping_list WHERE inventory_id=?`, id).Scan(&onList)
	if onList != 1 {
		t.Errorf("expected out-of-stock item on shopping list, got %d rows", onList)
	}
}

func TestDeductEmptyLotFoldsIntoSibling(t *testing.T) {
	mux, db := newSchemaMux(t)
	first := insertLot(t, db, "flour", 200, "g", "2026-11-01", "FLOUR1")
	second := insertLot(t, db, "flour", 500, "g", "2026-12-01", "")
	recipeID := linkRecipe(t, db, first)
	if _, err := db.Exec(`INSERT INTO inventory_skus (inventory_id, barcode, quantity_per_scan) VALUES (?, 'FLOUR-ALIAS', 1)`, first); err != nil {
		t.Fatal(err)
	}

	w := call(t, mux, http.MethodPatch, "/api/inventory/"+strconv.FormatInt(first, 10)+"?source=manual", `{"quantity":0,"location_id":`+strconv.FormatInt(first, 10)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var exists int
	db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE id=?`, first).Scan(&exists)
	if exists != 0 {
		t.Errorf("expected emptied lot to be removed when a sibling remains")
	}
	if got := ingredientInventoryID(t, db, recipeID); !got.Valid || got.Int64 != second {
		t.Errorf("expected recipe to relink to sibling %d, got %+v", second, got)
	}
	var barcode string
	db.QueryRow(`SELECT barcode FROM inventory WHERE id=?`, second).Scan(&barcode)
	if barcode != "FLOUR1" {
		t.Errorf("expected primary barcode to move to sibling, got %q", barcode)
	}
	var aliasOwner int64
	db.QueryRow(`SELECT inventory_id FROM inventory_skus WHERE barcode='FLOUR-ALIAS'`).Scan(&aliasOwner)
	if aliasOwner != second {
		t.Errorf("expected SKU alias to move to sibling, owner=%d", aliasOwner)
	}
}

func TestDeleteLinkedItem(t *testing.T) {
	mux, db := newSchemaMux(t)
	id := insertLot(t, db, "flour", 500, "g", "", "")
	recipeID := linkRecipe(t, db, id)
	if _, err := db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit) VALUES (?, 'flour', 1, 'g')`, id); err != nil {
		t.Fatal(err)
	}

	w := call(t, mux, http.MethodDelete, "/api/inventory/"+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if got := ingredientInventoryID(t, db, recipeID); got.Valid {
		t.Errorf("expected recipe link cleared, got %+v", got)
	}

	w = call(t, mux, http.MethodDelete, "/api/inventory/"+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing item, got %d", w.Code)
	}
}

func TestDeleteLinkedItemRelinksToSibling(t *testing.T) {
	mux, db := newSchemaMux(t)
	id := insertLot(t, db, "flour", 500, "g", "2026-11-01", "")
	sibling := insertLot(t, db, "flour", 300, "g", "2026-12-01", "")
	recipeID := linkRecipe(t, db, id)

	w := call(t, mux, http.MethodDelete, "/api/inventory/"+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if got := ingredientInventoryID(t, db, recipeID); !got.Valid || got.Int64 != sibling {
		t.Errorf("expected recipe to relink to sibling %d, got %+v", sibling, got)
	}
}

func TestMergeLinkedItem(t *testing.T) {
	mux, db := newSchemaMux(t)
	source := insertLot(t, db, "flour", 200, "g", "", "")
	target := insertLot(t, db, "all purpose flour", 500, "g", "", "")
	recipeID := linkRecipe(t, db, source)

	path := "/api/inventory/" + strconv.FormatInt(source, 10) + "/merge-into/" + strconv.FormatInt(target, 10)
	w := call(t, mux, http.MethodPost, path, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := ingredientInventoryID(t, db, recipeID); !got.Valid || got.Int64 != target {
		t.Errorf("expected recipe to relink to merge target %d, got %+v", target, got)
	}
}
