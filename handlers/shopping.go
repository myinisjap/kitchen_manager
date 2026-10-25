package handlers

import (
	"database/sql"
	"net/http"

	"kitchen_manager/services"
	"kitchen_manager/units"
)

func RegisterShopping(mux *http.ServeMux, db *sql.DB, hub ...*Hub) {
	var wsHub *Hub
	if len(hub) > 0 {
		wsHub = hub[0]
	}
	broadcastShopping := func() {
		if wsHub != nil {
			wsHub.Broadcast("shopping_updated")
		}
	}

	registerPutAway(mux, db, func() {
		if wsHub != nil {
			wsHub.Broadcast("inventory_updated")
			wsHub.Broadcast("shopping_updated")
		}
	})

	mux.HandleFunc("POST /api/shopping/generate-from-thresholds", func(w http.ResponseWriter, r *http.Request) {
		added, err := services.GenerateFromThresholds(db)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"added": added})
		broadcastShopping()
	})

	mux.HandleFunc("DELETE /api/shopping/checked", func(w http.ResponseWriter, r *http.Request) {
		if _, err := db.Exec(`DELETE FROM shopping_list_sources WHERE shopping_id IN (SELECT id FROM shopping_list WHERE checked=1)`); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if _, err := db.Exec(`DELETE FROM shopping_list WHERE checked=1`); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
		broadcastShopping()
	})

	mux.HandleFunc("POST /api/shopping/", func(w http.ResponseWriter, r *http.Request) {
		var item struct {
			InventoryID    *int64  `json:"inventory_id"`
			Name           string  `json:"name"`
			QuantityNeeded float64 `json:"quantity_needed"`
			Unit           string  `json:"unit"`
			Source         string  `json:"source"`
		}
		if err := ReadJSON(r, &item); err != nil || item.Name == "" {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if item.Source == "" {
			item.Source = "manual"
		}
		if item.QuantityNeeded == 0 {
			item.QuantityNeeded = 1
		}
		if item.Unit != "" && !units.IsValid(units.Unit(item.Unit)) {
			WriteError(w, http.StatusBadRequest, "invalid unit; valid units: g, kg, oz, lb, ml, L, cup, tbsp, tsp, piece, clove, can, jar, bunch")
			return
		}
		res, err := db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,?)`,
			item.InventoryID, item.Name, item.QuantityNeeded, item.Unit, item.Source)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		id, _ := res.LastInsertId()
		WriteJSON(w, http.StatusCreated, map[string]any{
			"id": id, "inventory_id": item.InventoryID, "name": item.Name,
			"quantity_needed": item.QuantityNeeded, "unit": item.Unit,
			"checked": false, "source": item.Source,
			"sources": []map[string]any{},
		})
		broadcastShopping()
	})

	mux.HandleFunc("GET /api/shopping/", func(w http.ResponseWriter, r *http.Request) {
		showChecked := r.URL.Query().Get("show_checked") == "true"
		q := `SELECT id,inventory_id,name,quantity_needed,unit,checked,source FROM shopping_list`
		if !showChecked {
			q += ` WHERE checked=0`
		}
		rows, err := db.Query(q)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer rows.Close()
		items, err := scanShoppingRows(rows)
		rows.Close()
		if err == nil {
			err = attachSources(db, items)
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, items)
	})

	mux.HandleFunc("PATCH /api/shopping/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var patch map[string]any
		if err := ReadJSON(r, &patch); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if checked, ok := patch["checked"]; ok {
			val := 0
			if b, ok := checked.(bool); ok && b {
				val = 1
			}
			if _, err := db.Exec(`UPDATE shopping_list SET checked=? WHERE id=?`, val, id); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		if qty, ok := patch["quantity_needed"]; ok {
			if _, err := db.Exec(`UPDATE shopping_list SET quantity_needed=? WHERE id=?`, qty, id); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		row := db.QueryRow(`SELECT id,inventory_id,name,quantity_needed,unit,checked,source FROM shopping_list WHERE id=?`, id)
		item, err := scanShoppingRow(row)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := attachSources(db, []map[string]any{item}); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, item)
		broadcastShopping()
	})

	mux.HandleFunc("DELETE /api/shopping/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		if _, err := db.Exec(`DELETE FROM shopping_list_sources WHERE shopping_id=?`, id); err != nil {
			WriteServerError(w, r, err)
			return
		}
		res, err := db.Exec(`DELETE FROM shopping_list WHERE id=?`, id)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		broadcastShopping()
	})
}

func scanShoppingRow(row *sql.Row) (map[string]any, error) {
	var id int64
	var invID sql.NullInt64
	var name, unit, source string
	var qty float64
	var checked int
	err := row.Scan(&id, &invID, &name, &qty, &unit, &checked, &source)
	if err != nil {
		return nil, err
	}
	var invIDVal any
	if invID.Valid {
		invIDVal = invID.Int64
	}
	return map[string]any{
		"id": id, "inventory_id": invIDVal, "name": name,
		"quantity_needed": qty, "unit": unit, "checked": checked == 1, "source": source,
	}, nil
}

func scanShoppingRows(rows *sql.Rows) ([]map[string]any, error) {
	var items []map[string]any
	for rows.Next() {
		var id int64
		var invID sql.NullInt64
		var name, unit, source string
		var qty float64
		var checked int
		if err := rows.Scan(&id, &invID, &name, &qty, &unit, &checked, &source); err != nil {
			return nil, err
		}
		var invIDVal any
		if invID.Valid {
			invIDVal = invID.Int64
		}
		items = append(items, map[string]any{
			"id": id, "inventory_id": invIDVal, "name": name,
			"quantity_needed": qty, "unit": unit, "checked": checked == 1, "source": source,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if items == nil {
		return []map[string]any{}, nil
	}
	return items, nil
}

// attachSources adds a "sources" array to each item (empty when none).
// Must be called with no rows open (single DB connection).
func attachSources(db *sql.DB, items []map[string]any) error {
	by := map[int64][]map[string]any{}
	rows, err := db.Query(`SELECT shopping_id, source, label, quantity FROM shopping_list_sources ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var src, label string
		var q float64
		if err := rows.Scan(&id, &src, &label, &q); err != nil {
			return err
		}
		by[id] = append(by[id], map[string]any{"source": src, "label": label, "quantity": q})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range items {
		s := by[it["id"].(int64)]
		if s == nil {
			s = []map[string]any{}
		}
		it["sources"] = s
	}
	return nil
}
