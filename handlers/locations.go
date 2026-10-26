package handlers

import (
	"database/sql"
	"net/http"
	"strings"
)

// Rooms contain storage units; inventory items point at a storage unit.
//
// Deletion policy (rooms and storage units alike): refuse with 409 while any
// inventory row still references a unit being removed. Silently nulling the
// reference would lose where the food actually is with no way to recover it,
// and the user can always move or delete those items first.
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
			WriteError(w, http.StatusInternalServerError, err.Error())
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
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		WriteJSON(w, http.StatusCreated, room{ID: id, Name: name, StorageUnits: []storageUnit{}})
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
			WriteError(w, http.StatusInternalServerError, err.Error())
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
		if err := db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE storage_unit_id IN (SELECT id FROM storage_units WHERE room_id=?)`, id).Scan(&refs); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if refs > 0 {
			WriteError(w, http.StatusConflict, "room still has inventory items in its storage units; move or delete them first")
			return
		}
		// storage_units.room_id cascades, so the room's (now empty) units go with it.
		res, err := db.Exec(`DELETE FROM rooms WHERE id=?`, id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
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
			WriteError(w, http.StatusInternalServerError, err.Error())
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
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		WriteJSON(w, http.StatusCreated, storageUnit{ID: id, Name: name})
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
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			WriteError(w, http.StatusNotFound, "storage unit not found")
			return
		}
		WriteJSON(w, http.StatusOK, storageUnit{ID: id, Name: name})
		broadcast()
	})

	mux.HandleFunc("DELETE /api/storage-units/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var refs int
		if err := db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE storage_unit_id=?`, id).Scan(&refs); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if refs > 0 {
			WriteError(w, http.StatusConflict, "storage unit still has inventory items; move or delete them first")
			return
		}
		res, err := db.Exec(`DELETE FROM storage_units WHERE id=?`, id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
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

type storageUnit struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type room struct {
	ID           int64         `json:"id"`
	Name         string        `json:"name"`
	StorageUnits []storageUnit `json:"storage_units"`
}

// listRooms returns every room with its storage units nested, ordered by name.
func listRooms(db *sql.DB) ([]room, error) {
	rows, err := db.Query(`SELECT r.id, r.name, u.id, u.name
		FROM rooms r LEFT JOIN storage_units u ON u.room_id = r.id
		ORDER BY r.name, u.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rooms := []room{}
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
			rooms = append(rooms, room{ID: roomID, Name: roomName, StorageUnits: []storageUnit{}})
			idx = len(rooms) - 1
			byID[roomID] = idx
		}
		if unitID.Valid {
			rooms[idx].StorageUnits = append(rooms[idx].StorageUnits, storageUnit{ID: unitID.Int64, Name: unitName.String})
		}
	}
	return rooms, rows.Err()
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
