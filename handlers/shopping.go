package handlers

import (
	"database/sql"
	"net/http"

	"kitchen_manager/services"
)

func RegisterShopping(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /api/shopping/generate-from-thresholds", func(w http.ResponseWriter, r *http.Request) {
		added, err := services.GenerateFromThresholds(db)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"added": added})
	})

	mux.HandleFunc("DELETE /api/shopping/checked", func(w http.ResponseWriter, r *http.Request) {
		if _, err := db.Exec(`DELETE FROM shopping_list WHERE checked=1`); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
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
		res, err := db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,?)`,
			item.InventoryID, item.Name, item.QuantityNeeded, item.Unit, item.Source)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		WriteJSON(w, http.StatusCreated, map[string]any{
			"id": id, "inventory_id": item.InventoryID, "name": item.Name,
			"quantity_needed": item.QuantityNeeded, "unit": item.Unit,
			"checked": false, "source": item.Source,
		})
	})

	mux.HandleFunc("GET /api/shopping/", func(w http.ResponseWriter, r *http.Request) {
		showChecked := r.URL.Query().Get("show_checked") == "true"
		q := `SELECT id,inventory_id,name,quantity_needed,unit,checked,source FROM shopping_list`
		if !showChecked {
			q += ` WHERE checked=0`
		}
		rows, err := db.Query(q)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		items, err := scanShoppingRows(rows)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
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
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if qty, ok := patch["quantity_needed"]; ok {
			if _, err := db.Exec(`UPDATE shopping_list SET quantity_needed=? WHERE id=?`, qty, id); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
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
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, item)
	})

	mux.HandleFunc("DELETE /api/shopping/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		res, err := db.Exec(`DELETE FROM shopping_list WHERE id=?`, id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
