package handlers

import (
	"database/sql"
	"net/http"
	"strings"
)

// Rooms contain storage units; stock lots point at a storage unit.
//
// Deletion policy (rooms and storage units alike): refuse with 409 while any
// stock lot still references a unit being removed. Silently nulling the
// reference would lose where the food actually is with no way to recover it,
// and the user can always move or delete those lots first.
//
// See docs/rooms-storage-units-intent.md.
func RegisterLocations(mux *http.ServeMux, db *sql.DB, hub ...*Hub) {
	var wsHub *Hub
	if len(hub) > 0 {
		wsHub = hub[0]
	}
	broadcast := func() {
		if wsHub != nil {
			wsHub.Broadcast("inventory_updated")
		}
	}

	// readName decodes {"name": "..."} and trims it, writing a 400 on failure.
	readName := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		var body struct {
			Name string `json:"name"`
		}
		if err := ReadJSON(r, &body); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return "", false
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			WriteError(w, http.StatusBadRequest, "name is required")
			return "", false
		}
		return name, true
	}

	mux.HandleFunc("GET /api/rooms", func(w http.ResponseWriter, r *http.Request) {
		rooms, err := listRooms(db)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, rooms)
	})

	mux.HandleFunc("POST /api/rooms", func(w http.ResponseWriter, r *http.Request) {
		name, ok := readName(w, r)
		if !ok {
			return
		}
		res, err := db.Exec(`INSERT INTO rooms (name) VALUES (?)`, name)
		if err != nil {
			if isUniqueViolation(err) {
				WriteError(w, http.StatusConflict, "a room with that name already exists")
				return
			}
			WriteServerError(w, r, err)
			return
		}
		id, _ := res.LastInsertId()
		WriteJSON(w, http.StatusCreated, Room{ID: id, Name: name, StorageUnits: []StorageUnit{}})
		broadcast()
	})

	mux.HandleFunc("PATCH /api/rooms/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		name, ok := readName(w, r)
		if !ok {
			return
		}
		res, err := db.Exec(`UPDATE rooms SET name=? WHERE id=?`, name, id)
		if err != nil {
			if isUniqueViolation(err) {
				WriteError(w, http.StatusConflict, "a room with that name already exists")
				return
			}
			WriteServerError(w, r, err)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			WriteError(w, http.StatusNotFound, "room not found")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"id": id, "name": name})
		broadcast()
	})

	mux.HandleFunc("DELETE /api/rooms/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var refs int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM stock_lots WHERE storage_unit_id IN (SELECT id FROM storage_units WHERE room_id=?)`,
			id,
		).Scan(&refs); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if refs > 0 {
			WriteError(w, http.StatusConflict, "room still has stock in its storage units; move or delete it first")
			return
		}
		// storage_units.room_id cascades, so the room's (now empty) units go with it.
		res, err := db.Exec(`DELETE FROM rooms WHERE id=?`, id)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			WriteError(w, http.StatusNotFound, "room not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		broadcast()
	})

	mux.HandleFunc("POST /api/rooms/{room_id}/storage-units", func(w http.ResponseWriter, r *http.Request) {
		roomID, ok := pathIDFromPattern(r, "room_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid room_id")
			return
		}
		name, ok := readName(w, r)
		if !ok {
			return
		}
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM rooms WHERE id=?`, roomID).Scan(&exists); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if exists == 0 {
			WriteError(w, http.StatusNotFound, "room not found")
			return
		}
		res, err := db.Exec(`INSERT INTO storage_units (room_id, name) VALUES (?,?)`, roomID, name)
		if err != nil {
			if isUniqueViolation(err) {
				WriteError(w, http.StatusConflict, "that room already has a storage unit with this name")
				return
			}
			WriteServerError(w, r, err)
			return
		}
		id, _ := res.LastInsertId()
		WriteJSON(w, http.StatusCreated, StorageUnit{ID: id, Name: name})
		broadcast()
	})

	mux.HandleFunc("PATCH /api/storage-units/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		name, ok := readName(w, r)
		if !ok {
			return
		}
		res, err := db.Exec(`UPDATE storage_units SET name=? WHERE id=?`, name, id)
		if err != nil {
			if isUniqueViolation(err) {
				WriteError(w, http.StatusConflict, "that room already has a storage unit with this name")
				return
			}
			WriteServerError(w, r, err)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			WriteError(w, http.StatusNotFound, "storage unit not found")
			return
		}
		WriteJSON(w, http.StatusOK, StorageUnit{ID: id, Name: name})
		broadcast()
	})

	mux.HandleFunc("DELETE /api/storage-units/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var refs int
		if err := db.QueryRow(`SELECT COUNT(*) FROM stock_lots WHERE storage_unit_id=?`, id).Scan(&refs); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if refs > 0 {
			WriteError(w, http.StatusConflict, "storage unit still has stock; move or delete it first")
			return
		}
		res, err := db.Exec(`DELETE FROM storage_units WHERE id=?`, id)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			WriteError(w, http.StatusNotFound, "storage unit not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		broadcast()
	})
}

// StorageUnit is a place within a room. Room is set only where a lot carries
// its unit nested, so a client can render "Kitchen › Fridge" without a second
// lookup; it is omitted inside a Room's own StorageUnits list.
type StorageUnit struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Room string `json:"room,omitempty"`
}

type Room struct {
	ID           int64         `json:"id"`
	Name         string        `json:"name"`
	StorageUnits []StorageUnit `json:"storage_units"`
}

// listRooms returns every room with its storage units nested, ordered by name.
// Rooms with no units get an empty slice rather than null, so clients can
// iterate without a nil check.
func listRooms(db *sql.DB) ([]Room, error) {
	rows, err := db.Query(`SELECT r.id, r.name, u.id, u.name
		FROM rooms r LEFT JOIN storage_units u ON u.room_id = r.id
		ORDER BY r.name, u.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rooms := []Room{}
	byID := map[int64]int{}
	for rows.Next() {
		var roomID int64
		var roomName string
		var unitID sql.NullInt64
		var unitName sql.NullString
		if err := rows.Scan(&roomID, &roomName, &unitID, &unitName); err != nil {
			return nil, err
		}
		idx, seen := byID[roomID]
		if !seen {
			rooms = append(rooms, Room{ID: roomID, Name: roomName, StorageUnits: []StorageUnit{}})
			idx = len(rooms) - 1
			byID[roomID] = idx
		}
		if unitID.Valid {
			rooms[idx].StorageUnits = append(rooms[idx].StorageUnits,
				StorageUnit{ID: unitID.Int64, Name: unitName.String})
		}
	}
	return rooms, rows.Err()
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
