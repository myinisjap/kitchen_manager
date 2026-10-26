package handlers_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/database"
	"kitchen_manager/internal/testutil"
)

// locMux wires the location handlers alongside inventory, since several of
// these behaviours are about how lots and locations interact.
func locMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	t.Helper()
	mux, db := newMux(t)
	handlers.RegisterLocations(mux, db)
	return mux, db
}

func mustJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	return v
}

func TestRoomsCRUD(t *testing.T) {
	mux, _ := locMux(t)

	// Empty list encodes as [], not null.
	w := call(t, mux, "GET", "/api/rooms", "")
	if w.Code != 200 || w.Body.String() == "null\n" {
		t.Fatalf("empty list: %d %q", w.Code, w.Body.String())
	}

	w = call(t, mux, "POST", "/api/rooms", `{"name":"  Kitchen  "}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	room := mustJSON[handlers.Room](t, w.Body.Bytes())
	if room.Name != "Kitchen" {
		t.Errorf("name not trimmed: %q", room.Name)
	}
	if room.StorageUnits == nil {
		t.Error("storage_units should be [] not null")
	}

	// Duplicate room name is a 409, not a 500.
	if w = call(t, mux, "POST", "/api/rooms", `{"name":"Kitchen"}`); w.Code != 409 {
		t.Errorf("duplicate room: want 409, got %d", w.Code)
	}
	// Blank and whitespace-only names are rejected.
	for _, body := range []string{`{"name":""}`, `{"name":"   "}`} {
		if w = call(t, mux, "POST", "/api/rooms", body); w.Code != 400 {
			t.Errorf("blank name %s: want 400, got %d", body, w.Code)
		}
	}
	if w = call(t, mux, "POST", "/api/rooms", `not json`); w.Code != 400 {
		t.Errorf("bad body: want 400, got %d", w.Code)
	}

	// Units nest under their room, and the same unit name may repeat across rooms.
	w = call(t, mux, "POST", fmt.Sprintf("/api/rooms/%d/storage-units", room.ID), `{"name":"Fridge"}`)
	if w.Code != 201 {
		t.Fatalf("create unit: %d %s", w.Code, w.Body)
	}
	if w = call(t, mux, "POST", fmt.Sprintf("/api/rooms/%d/storage-units", room.ID), `{"name":"Fridge"}`); w.Code != 409 {
		t.Errorf("duplicate unit in same room: want 409, got %d", w.Code)
	}
	w = call(t, mux, "POST", "/api/rooms", `{"name":"Garage"}`)
	garage := mustJSON[handlers.Room](t, w.Body.Bytes())
	if w = call(t, mux, "POST", fmt.Sprintf("/api/rooms/%d/storage-units", garage.ID), `{"name":"Fridge"}`); w.Code != 201 {
		t.Errorf("same unit name in another room should be allowed, got %d", w.Code)
	}
	// Unknown room when creating a unit.
	if w = call(t, mux, "POST", "/api/rooms/9999/storage-units", `{"name":"X"}`); w.Code != 404 {
		t.Errorf("unit under missing room: want 404, got %d", w.Code)
	}

	// List nests units under rooms, ordered by name.
	w = call(t, mux, "GET", "/api/rooms", "")
	rooms := mustJSON[[]handlers.Room](t, w.Body.Bytes())
	if len(rooms) != 2 || rooms[0].Name != "Garage" || rooms[1].Name != "Kitchen" {
		t.Fatalf("rooms not ordered by name: %+v", rooms)
	}
	if len(rooms[1].StorageUnits) != 1 || rooms[1].StorageUnits[0].Name != "Fridge" {
		t.Errorf("kitchen units: %+v", rooms[1].StorageUnits)
	}

	// Rename, including the 404 and 409 paths.
	if w = call(t, mux, "PATCH", fmt.Sprintf("/api/rooms/%d", room.ID), `{"name":"Scullery"}`); w.Code != 200 {
		t.Errorf("rename room: %d %s", w.Code, w.Body)
	}
	if w = call(t, mux, "PATCH", "/api/rooms/9999", `{"name":"Nope"}`); w.Code != 404 {
		t.Errorf("rename missing room: want 404, got %d", w.Code)
	}
	if w = call(t, mux, "PATCH", fmt.Sprintf("/api/rooms/%d", garage.ID), `{"name":"Scullery"}`); w.Code != 409 {
		t.Errorf("rename to duplicate: want 409, got %d", w.Code)
	}

	// An empty room deletes, taking its (empty) units with it.
	if w = call(t, mux, "DELETE", fmt.Sprintf("/api/rooms/%d", room.ID), ""); w.Code != 204 {
		t.Errorf("delete empty room: want 204, got %d %s", w.Code, w.Body)
	}
	if w = call(t, mux, "DELETE", "/api/rooms/9999", ""); w.Code != 404 {
		t.Errorf("delete missing room: want 404, got %d", w.Code)
	}
}

// The deletion policy: refuse while stock still points at the unit, rather
// than cascading or silently nulling the link.
func TestDeleteRefusedWhileStockReferencesUnit(t *testing.T) {
	mux, db := locMux(t)

	w := call(t, mux, "POST", "/api/rooms", `{"name":"Kitchen"}`)
	room := mustJSON[handlers.Room](t, w.Body.Bytes())
	w = call(t, mux, "POST", fmt.Sprintf("/api/rooms/%d/storage-units", room.ID), `{"name":"Fridge"}`)
	unit := mustJSON[handlers.StorageUnit](t, w.Body.Bytes())

	_, lot := testutil.AddLot(t, db, "Milk", 1, "l")
	if _, err := db.Exec(`UPDATE stock_lots SET storage_unit_id=? WHERE id=?`, unit.ID, lot); err != nil {
		t.Fatal(err)
	}

	if w = call(t, mux, "DELETE", fmt.Sprintf("/api/storage-units/%d", unit.ID), ""); w.Code != 409 {
		t.Errorf("delete referenced unit: want 409, got %d %s", w.Code, w.Body)
	}
	if w = call(t, mux, "DELETE", fmt.Sprintf("/api/rooms/%d", room.ID), ""); w.Code != 409 {
		t.Errorf("delete room of referenced unit: want 409, got %d %s", w.Code, w.Body)
	}

	// The link must survive the refused deletes.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_lots WHERE storage_unit_id=?`, unit.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("link lost: %d lots still reference the unit", n)
	}

	// Once the stock moves away, both deletes succeed.
	if _, err := db.Exec(`UPDATE stock_lots SET storage_unit_id=NULL WHERE id=?`, lot); err != nil {
		t.Fatal(err)
	}
	if w = call(t, mux, "DELETE", fmt.Sprintf("/api/storage-units/%d", unit.ID), ""); w.Code != 204 {
		t.Errorf("delete freed unit: want 204, got %d %s", w.Code, w.Body)
	}
	if w = call(t, mux, "DELETE", fmt.Sprintf("/api/rooms/%d", room.ID), ""); w.Code != 204 {
		t.Errorf("delete emptied room: want 204, got %d %s", w.Code, w.Body)
	}
}

// Legacy free-text locations become storage units under "Unsorted", and the
// backfill is a no-op on a second run.
func TestLegacyLocationBackfill(t *testing.T) {
	mux, db := locMux(t)

	_, lotA := testutil.AddLot(t, db, "Peas", 2, "kg", testutil.WithLocation("Downstairs freezer"))
	_, lotB := testutil.AddLot(t, db, "Beans", 1, "kg", testutil.WithLocation("Downstairs freezer"))
	_, lotC := testutil.AddLot(t, db, "Salt", 1, "kg") // no location: stays unfiled
	// AddLot runs after migration, so clear the links and re-run the backfill.
	if _, err := db.Exec(`UPDATE stock_lots SET storage_unit_id=NULL`); err != nil {
		t.Fatal(err)
	}
	if err := database.Backfill(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	w := call(t, mux, "GET", "/api/rooms", "")
	rooms := mustJSON[[]handlers.Room](t, w.Body.Bytes())
	var unsorted *handlers.Room
	for i := range rooms {
		if rooms[i].Name == database.UnsortedRoom {
			unsorted = &rooms[i]
		}
	}
	if unsorted == nil {
		t.Fatalf("no %q room created: %+v", database.UnsortedRoom, rooms)
	}
	if len(unsorted.StorageUnits) != 1 || unsorted.StorageUnits[0].Name != "Downstairs freezer" {
		t.Fatalf("units under Unsorted: %+v", unsorted.StorageUnits)
	}

	// Both lots sharing that location link to the one unit; the unlocated one doesn't.
	unitID := unsorted.StorageUnits[0].ID
	for _, id := range []int64{lotA, lotB} {
		var got sql.NullInt64
		if err := db.QueryRow(`SELECT storage_unit_id FROM stock_lots WHERE id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !got.Valid || got.Int64 != unitID {
			t.Errorf("lot %d: want unit %d, got %v", id, unitID, got)
		}
	}
	var unfiled sql.NullInt64
	if err := db.QueryRow(`SELECT storage_unit_id FROM stock_lots WHERE id=?`, lotC).Scan(&unfiled); err != nil {
		t.Fatal(err)
	}
	if unfiled.Valid {
		t.Errorf("lot with no location should stay unfiled, got unit %d", unfiled.Int64)
	}

	// Idempotent: a second run creates nothing and relinks nothing.
	if err := database.Backfill(db); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	var units int
	if err := db.QueryRow(`SELECT COUNT(*) FROM storage_units`).Scan(&units); err != nil {
		t.Fatal(err)
	}
	if units != 1 {
		t.Errorf("second backfill duplicated units: %d", units)
	}

	// The legacy text is kept: it is the only record of the original string.
	var loc string
	if err := db.QueryRow(`SELECT location FROM stock_lots WHERE id=?`, lotA).Scan(&loc); err != nil {
		t.Fatal(err)
	}
	if loc != "Downstairs freezer" {
		t.Errorf("legacy location dropped: %q", loc)
	}
}

// A location name that exists in more than one room is ambiguous, so the
// backfill must leave those lots unfiled rather than guess a room.
func TestBackfillSkipsAmbiguousUnitName(t *testing.T) {
	mux, db := locMux(t)

	for _, room := range []string{"Kitchen", "Garage"} {
		w := call(t, mux, "POST", "/api/rooms", fmt.Sprintf(`{"name":%q}`, room))
		r := mustJSON[handlers.Room](t, w.Body.Bytes())
		if w = call(t, mux, "POST", fmt.Sprintf("/api/rooms/%d/storage-units", r.ID), `{"name":"Fridge"}`); w.Code != 201 {
			t.Fatalf("seed unit: %d %s", w.Code, w.Body)
		}
	}
	_, lot := testutil.AddLot(t, db, "Milk", 1, "l", testutil.WithLocation("Fridge"))
	if _, err := db.Exec(`UPDATE stock_lots SET storage_unit_id=NULL`); err != nil {
		t.Fatal(err)
	}
	if err := database.Backfill(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var got sql.NullInt64
	if err := db.QueryRow(`SELECT storage_unit_id FROM stock_lots WHERE id=?`, lot).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got.Valid {
		t.Errorf("ambiguous name should stay unfiled, got unit %d", got.Int64)
	}
	// And it must not have invented a third "Fridge" under Unsorted.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM storage_units WHERE name='Fridge'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("want the 2 seeded Fridge units, got %d", n)
	}
}

// Inventory responses carry both the nested storage_unit (with room name, so a
// client can render "Kitchen › Fridge") and the flat storage_unit_id.
func TestInventoryEndpointsReflectStorageUnit(t *testing.T) {
	mux, db := locMux(t)

	w := call(t, mux, "POST", "/api/rooms", `{"name":"Kitchen"}`)
	room := mustJSON[handlers.Room](t, w.Body.Bytes())
	w = call(t, mux, "POST", fmt.Sprintf("/api/rooms/%d/storage-units", room.ID), `{"name":"Fridge"}`)
	unit := mustJSON[handlers.StorageUnit](t, w.Body.Bytes())

	_, lot := testutil.AddLot(t, db, "Milk", 1, "l")
	if _, err := db.Exec(`UPDATE stock_lots SET storage_unit_id=? WHERE id=?`, unit.ID, lot); err != nil {
		t.Fatal(err)
	}

	w = call(t, mux, "GET", fmt.Sprintf("/api/inventory/%d", lot), "")
	got := mustJSON[handlers.Lot](t, w.Body.Bytes())
	if got.StorageUnitID == nil || *got.StorageUnitID != unit.ID {
		t.Fatalf("storage_unit_id: %v, want %d", got.StorageUnitID, unit.ID)
	}
	if got.StorageUnit == nil {
		t.Fatal("storage_unit should be nested on a filed lot")
	}
	if got.StorageUnit.Name != "Fridge" || got.StorageUnit.Room != "Kitchen" {
		t.Errorf("nested unit: %+v, want Kitchen > Fridge", got.StorageUnit)
	}

	// An unfiled lot reports a null id and omits the nested object entirely.
	_, bare := testutil.AddLot(t, db, "Salt", 1, "kg")
	if _, err := db.Exec(`UPDATE stock_lots SET storage_unit_id=NULL WHERE id=?`, bare); err != nil {
		t.Fatal(err)
	}
	w = call(t, mux, "GET", fmt.Sprintf("/api/inventory/%d", bare), "")
	raw := mustJSON[map[string]any](t, w.Body.Bytes())
	if v, ok := raw["storage_unit_id"]; !ok || v != nil {
		t.Errorf("unfiled lot storage_unit_id: want present and null, got %v (present=%v)", v, ok)
	}
	if _, ok := raw["storage_unit"]; ok {
		t.Error("unfiled lot should omit storage_unit")
	}
}
