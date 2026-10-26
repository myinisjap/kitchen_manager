package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"kitchen_manager/internal/database"
)

// do issues a request against the mux and returns the recorder.
func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestRoomsCRUD(t *testing.T) {
	mux, _ := newMux(t)

	// Create
	w := do(t, mux, "POST", "/api/rooms", `{"name":"Kitchen"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create room: want 201, got %d: %s", w.Code, w.Body)
	}
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	roomID := int64(created["id"].(float64))
	if created["name"] != "Kitchen" {
		t.Errorf("want name Kitchen, got %v", created["name"])
	}
	if su, ok := created["storage_units"].([]any); !ok || len(su) != 0 {
		t.Errorf("want empty storage_units array, got %v", created["storage_units"])
	}

	// Duplicate name → 409
	if w := do(t, mux, "POST", "/api/rooms", `{"name":"Kitchen"}`); w.Code != http.StatusConflict {
		t.Errorf("duplicate room: want 409, got %d", w.Code)
	}

	// Blank name → 400
	if w := do(t, mux, "POST", "/api/rooms", `{"name":"   "}`); w.Code != http.StatusBadRequest {
		t.Errorf("blank room name: want 400, got %d", w.Code)
	}

	// Add storage units
	unitPath := "/api/rooms/" + strconv.FormatInt(roomID, 10) + "/storage-units"
	w2 := do(t, mux, "POST", unitPath, `{"name":"Kitchen 1"}`)
	if w2.Code != http.StatusCreated {
		t.Fatalf("create unit: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var unit map[string]any
	json.Unmarshal(w2.Body.Bytes(), &unit)
	unitID := int64(unit["id"].(float64))

	if w := do(t, mux, "POST", unitPath, `{"name":"Kitchen 2"}`); w.Code != http.StatusCreated {
		t.Fatalf("create second unit: want 201, got %d", w.Code)
	}
	// Duplicate unit name within the same room → 409
	if w := do(t, mux, "POST", unitPath, `{"name":"Kitchen 1"}`); w.Code != http.StatusConflict {
		t.Errorf("duplicate unit in room: want 409, got %d", w.Code)
	}
	// Unit under a missing room → 404
	if w := do(t, mux, "POST", "/api/rooms/9999/storage-units", `{"name":"Nowhere"}`); w.Code != http.StatusNotFound {
		t.Errorf("unit in missing room: want 404, got %d", w.Code)
	}

	// Same unit name in a different room is allowed
	w3 := do(t, mux, "POST", "/api/rooms", `{"name":"Garage"}`)
	var garage map[string]any
	json.Unmarshal(w3.Body.Bytes(), &garage)
	garageID := int64(garage["id"].(float64))
	if w := do(t, mux, "POST", "/api/rooms/"+strconv.FormatInt(garageID, 10)+"/storage-units", `{"name":"Kitchen 1"}`); w.Code != http.StatusCreated {
		t.Errorf("same unit name in another room: want 201, got %d", w.Code)
	}

	// List: nested shape, ordered by room name then unit name
	rooms := listRoomsAPI(t, mux)
	if len(rooms) != 2 {
		t.Fatalf("want 2 rooms, got %d", len(rooms))
	}
	if rooms[0]["name"] != "Garage" || rooms[1]["name"] != "Kitchen" {
		t.Errorf("want rooms ordered Garage, Kitchen; got %v, %v", rooms[0]["name"], rooms[1]["name"])
	}
	kitchenUnits := rooms[1]["storage_units"].([]any)
	if len(kitchenUnits) != 2 {
		t.Fatalf("want 2 units in Kitchen, got %d", len(kitchenUnits))
	}
	if kitchenUnits[0].(map[string]any)["name"] != "Kitchen 1" {
		t.Errorf("want first unit 'Kitchen 1', got %v", kitchenUnits[0])
	}

	// Rename room and unit
	if w := do(t, mux, "PATCH", "/api/rooms/"+strconv.FormatInt(roomID, 10), `{"name":"Main Kitchen"}`); w.Code != http.StatusOK {
		t.Fatalf("rename room: want 200, got %d: %s", w.Code, w.Body)
	}
	if w := do(t, mux, "PATCH", "/api/storage-units/"+strconv.FormatInt(unitID, 10), `{"name":"Upper Cabinet"}`); w.Code != http.StatusOK {
		t.Fatalf("rename unit: want 200, got %d: %s", w.Code, w.Body)
	}
	rooms = listRoomsAPI(t, mux)
	var renamed map[string]any
	for _, r := range rooms {
		if r["name"] == "Main Kitchen" {
			renamed = r
		}
	}
	if renamed == nil {
		t.Fatal("renamed room not found")
	}
	if renamed["storage_units"].([]any)[1].(map[string]any)["name"] != "Upper Cabinet" {
		t.Errorf("unit rename not reflected: %v", renamed["storage_units"])
	}

	// Missing ids → 404
	if w := do(t, mux, "PATCH", "/api/rooms/9999", `{"name":"Ghost"}`); w.Code != http.StatusNotFound {
		t.Errorf("rename missing room: want 404, got %d", w.Code)
	}
	if w := do(t, mux, "DELETE", "/api/storage-units/9999", ""); w.Code != http.StatusNotFound {
		t.Errorf("delete missing unit: want 404, got %d", w.Code)
	}

	// Delete an unreferenced unit, then the room (cascade takes its remaining units)
	if w := do(t, mux, "DELETE", "/api/storage-units/"+strconv.FormatInt(unitID, 10), ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete unit: want 204, got %d: %s", w.Code, w.Body)
	}
	if w := do(t, mux, "DELETE", "/api/rooms/"+strconv.FormatInt(roomID, 10), ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete room: want 204, got %d: %s", w.Code, w.Body)
	}
	rooms = listRoomsAPI(t, mux)
	if len(rooms) != 1 || rooms[0]["name"] != "Garage" {
		t.Errorf("want only Garage left, got %v", rooms)
	}
}

func listRoomsAPI(t *testing.T, mux *http.ServeMux) []map[string]any {
	t.Helper()
	w := do(t, mux, "GET", "/api/rooms", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list rooms: want 200, got %d: %s", w.Code, w.Body)
	}
	var rooms []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rooms); err != nil {
		t.Fatalf("invalid rooms JSON: %v", err)
	}
	return rooms
}

// Deleting a room or storage unit that still holds inventory is refused with 409
// rather than silently orphaning the items.
func TestDeleteRefusedWhileInventoryReferencesUnit(t *testing.T) {
	mux, db := newMux(t)

	unitID := newStorageUnit(t, db, "Kitchen", "Fridge")
	var roomID int64
	if err := db.QueryRow(`SELECT room_id FROM storage_units WHERE id=?`, unitID).Scan(&roomID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,storage_unit_id) VALUES ('milk',1,'L',?)`, unitID); err != nil {
		t.Fatal(err)
	}

	if w := do(t, mux, "DELETE", "/api/storage-units/"+strconv.FormatInt(unitID, 10), ""); w.Code != http.StatusConflict {
		t.Errorf("delete referenced unit: want 409, got %d: %s", w.Code, w.Body)
	}
	if w := do(t, mux, "DELETE", "/api/rooms/"+strconv.FormatInt(roomID, 10), ""); w.Code != http.StatusConflict {
		t.Errorf("delete room with referenced unit: want 409, got %d: %s", w.Code, w.Body)
	}

	// Nothing was removed, and the item still points at its unit.
	var units, rooms int
	db.QueryRow(`SELECT COUNT(*) FROM storage_units`).Scan(&units)
	db.QueryRow(`SELECT COUNT(*) FROM rooms`).Scan(&rooms)
	if units != 1 || rooms != 1 {
		t.Errorf("want hierarchy intact after refused deletes, got %d units / %d rooms", units, rooms)
	}
	var linked int64
	if err := db.QueryRow(`SELECT storage_unit_id FROM inventory WHERE name='milk'`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != unitID {
		t.Errorf("want item still linked to unit %d, got %d", unitID, linked)
	}

	// Once the item is gone, both deletes succeed.
	if _, err := db.Exec(`DELETE FROM inventory WHERE name='milk'`); err != nil {
		t.Fatal(err)
	}
	if w := do(t, mux, "DELETE", "/api/rooms/"+strconv.FormatInt(roomID, 10), ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete room after clearing inventory: want 204, got %d: %s", w.Code, w.Body)
	}
	db.QueryRow(`SELECT COUNT(*) FROM storage_units`).Scan(&units)
	if units != 0 {
		t.Errorf("want units cascade-deleted with room, got %d", units)
	}
}

// The backfill converts legacy free-text inventory.location values into storage
// units under the "Unsorted" room, and is safe to re-run on every startup.
func TestLegacyLocationBackfill(t *testing.T) {
	db := newTestDB(t)

	// Simulate a pre-migration database: rows with location text and no unit.
	for _, seed := range []struct {
		name string
		loc  string
	}{
		{"milk", "Fridge"},
		{"butter", "Fridge"}, // same location → same unit, not a duplicate
		{"flour", "Pantry"},
		{"salt", ""}, // no location → stays unassigned
	} {
		if _, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,location) VALUES (?,1,'g',?)`, seed.name, seed.loc); err != nil {
			t.Fatal(err)
		}
	}

	runBackfill := func() {
		t.Helper()
		if err := database.Backfill(db); err != nil {
			t.Fatal(err)
		}
	}
	runBackfill()

	assertState := func(stage string) {
		t.Helper()
		var rooms int
		db.QueryRow(`SELECT COUNT(*) FROM rooms`).Scan(&rooms)
		if rooms != 1 {
			t.Errorf("%s: want 1 room, got %d", stage, rooms)
		}
		var roomName string
		db.QueryRow(`SELECT name FROM rooms`).Scan(&roomName)
		if roomName != database.UnsortedRoom {
			t.Errorf("%s: want room %q, got %q", stage, database.UnsortedRoom, roomName)
		}
		var units int
		db.QueryRow(`SELECT COUNT(*) FROM storage_units`).Scan(&units)
		if units != 2 {
			t.Errorf("%s: want 2 storage units (Fridge, Pantry), got %d", stage, units)
		}

		// Rows that shared a location share a unit.
		var milkUnit, butterUnit, flourUnit int64
		db.QueryRow(`SELECT storage_unit_id FROM inventory WHERE name='milk'`).Scan(&milkUnit)
		db.QueryRow(`SELECT storage_unit_id FROM inventory WHERE name='butter'`).Scan(&butterUnit)
		db.QueryRow(`SELECT storage_unit_id FROM inventory WHERE name='flour'`).Scan(&flourUnit)
		if milkUnit == 0 || milkUnit != butterUnit {
			t.Errorf("%s: want milk and butter in the same unit, got %d and %d", stage, milkUnit, butterUnit)
		}
		if flourUnit == milkUnit {
			t.Errorf("%s: want flour in its own unit, got %d", stage, flourUnit)
		}
		var fridgeName string
		db.QueryRow(`SELECT name FROM storage_units WHERE id=?`, milkUnit).Scan(&fridgeName)
		if fridgeName != "Fridge" {
			t.Errorf("%s: want unit named Fridge, got %q", stage, fridgeName)
		}

		// An item with no legacy location stays unassigned.
		var saltUnit any
		db.QueryRow(`SELECT storage_unit_id FROM inventory WHERE name='salt'`).Scan(&saltUnit)
		if saltUnit != nil {
			t.Errorf("%s: want salt unassigned, got %v", stage, saltUnit)
		}
	}
	assertState("first run")

	// Idempotent: re-running creates no duplicate rooms or units.
	runBackfill()
	runBackfill()
	assertState("after re-runs")
}

// Every inventory read endpoint reports the storage unit through the same
// nested shape, and the filters key on storage_unit_id.
func TestInventoryEndpointsReflectStorageUnit(t *testing.T) {
	mux, db := newMux(t)

	fridge := newStorageUnit(t, db, "Kitchen", "Fridge")
	pantry := newStorageUnit(t, db, "Kitchen", "Pantry")
	soon := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	if _, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,storage_unit_id,expiration_date,barcode) VALUES ('milk',1,'L',?,?,'111')`, fridge, soon); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,storage_unit_id) VALUES ('flour',1,'kg',?)`, pantry); err != nil {
		t.Fatal(err)
	}
	// Unfiled item: storage_unit_id stays NULL.
	if _, err := db.Exec(`INSERT INTO inventory (name,quantity,unit) VALUES ('salt',1,'g')`); err != nil {
		t.Fatal(err)
	}

	decodeList := func(path string) []map[string]any {
		t.Helper()
		w := do(t, mux, "GET", path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d: %s", path, w.Code, w.Body)
		}
		var items []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
			t.Fatalf("%s: invalid JSON: %v", path, err)
		}
		return items
	}
	// wantUnit asserts the nested shape on a single item.
	wantUnit := func(label string, item map[string]any, unitName, roomName string) {
		t.Helper()
		su, ok := item["storage_unit"].(map[string]any)
		if !ok {
			t.Fatalf("%s: want nested storage_unit, got %v", label, item["storage_unit"])
		}
		if su["name"] != unitName || su["room"] != roomName {
			t.Errorf("%s: want %s in %s, got %v", label, unitName, roomName, su)
		}
	}

	// List, unfiltered: unfiled item carries an explicit null.
	all := decodeList("/api/inventory/")
	if len(all) != 3 {
		t.Fatalf("list: want 3 items, got %d", len(all))
	}
	for _, item := range all {
		if item["name"] == "salt" {
			if item["storage_unit"] != nil || item["storage_unit_id"] != nil {
				t.Errorf("unfiled item: want null storage unit, got %v / %v", item["storage_unit"], item["storage_unit_id"])
			}
		}
	}

	// List filtered by storage_unit_id, and by the __none__ sentinel.
	filtered := decodeList("/api/inventory/?storage_unit_id=" + strconv.FormatInt(fridge, 10))
	if len(filtered) != 1 || filtered[0]["name"] != "milk" {
		t.Fatalf("unit filter: want only milk, got %v", filtered)
	}
	wantUnit("list filter", filtered[0], "Fridge", "Kitchen")

	none := decodeList("/api/inventory/?storage_unit_id=__none__")
	if len(none) != 1 || none[0]["name"] != "salt" {
		t.Errorf("__none__ filter: want only salt, got %v", none)
	}
	if w := do(t, mux, "GET", "/api/inventory/?storage_unit_id=abc", ""); w.Code != http.StatusBadRequest {
		t.Errorf("non-numeric storage_unit_id: want 400, got %d", w.Code)
	}

	// Expiring and similar carry the same shape.
	expiring := decodeList("/api/inventory/expiring?days=7")
	if len(expiring) != 1 {
		t.Fatalf("expiring: want 1 item, got %d", len(expiring))
	}
	wantUnit("expiring", expiring[0], "Fridge", "Kitchen")

	similar := decodeList("/api/inventory/similar?name=milk")
	if len(similar) != 1 {
		t.Fatalf("similar: want 1 item, got %d", len(similar))
	}
	wantUnit("similar", similar[0], "Fridge", "Kitchen")

	// Grouped: __none__ selects the unfiled row and reports a null storage_unit.
	groupedNone := decodeList("/api/inventory/grouped?storage_unit_id=__none__")
	if len(groupedNone) != 1 || groupedNone[0]["name"] != "salt" {
		t.Fatalf("grouped __none__: want salt group, got %v", groupedNone)
	}
	entry := groupedNone[0]["locations"].([]any)[0].(map[string]any)
	if entry["storage_unit"] != nil {
		t.Errorf("grouped __none__: want null storage_unit, got %v", entry["storage_unit"])
	}

	grouped := decodeList("/api/inventory/grouped?storage_unit_id=" + strconv.FormatInt(pantry, 10))
	if len(grouped) != 1 {
		t.Fatalf("grouped unit filter: want 1 group, got %d", len(grouped))
	}
	wantUnit("grouped", grouped[0]["locations"].([]any)[0].(map[string]any), "Pantry", "Kitchen")

	// PATCH can move an item to another storage unit.
	var saltID int64
	db.QueryRow(`SELECT id FROM inventory WHERE name='salt'`).Scan(&saltID)
	w := do(t, mux, "PATCH", "/api/inventory/"+strconv.FormatInt(saltID, 10),
		`{"storage_unit_id":`+strconv.FormatInt(pantry, 10)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patch storage_unit_id: want 200, got %d: %s", w.Code, w.Body)
	}
	var moved map[string]any
	json.Unmarshal(w.Body.Bytes(), &moved)
	wantUnit("patch", moved, "Pantry", "Kitchen")

	// POST creates an item filed directly into a unit.
	w2 := do(t, mux, "POST", "/api/inventory/",
		`{"name":"Eggs","quantity":12,"unit":"piece","storage_unit_id":`+strconv.FormatInt(fridge, 10)+`}`)
	if w2.Code != http.StatusCreated {
		t.Fatalf("create with storage_unit_id: want 201, got %d: %s", w2.Code, w2.Body)
	}
	var eggs map[string]any
	json.Unmarshal(w2.Body.Bytes(), &eggs)
	wantUnit("create", eggs, "Fridge", "Kitchen")
}
