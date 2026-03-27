package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

func RegisterInventory(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /api/inventory/", func(w http.ResponseWriter, r *http.Request) {
		var item struct {
			Name           string  `json:"name"`
			Quantity       float64 `json:"quantity"`
			Unit           string  `json:"unit"`
			Location       string  `json:"location"`
			ExpirationDate string  `json:"expiration_date"`
			LowThreshold   float64 `json:"low_threshold"`
			Barcode        string  `json:"barcode"`
		}
		if err := ReadJSON(r, &item); err != nil || item.Name == "" {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if item.LowThreshold == 0 {
			item.LowThreshold = 1
		}
		res, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,location,expiration_date,low_threshold,barcode) VALUES (?,?,?,?,?,?,?)`,
			item.Name, item.Quantity, item.Unit, item.Location, item.ExpirationDate, item.LowThreshold, item.Barcode)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		WriteJSON(w, http.StatusCreated, map[string]any{
			"id": id, "name": item.Name, "quantity": item.Quantity,
			"unit": item.Unit, "location": item.Location,
			"expiration_date": item.ExpirationDate, "low_threshold": item.LowThreshold,
			"barcode": item.Barcode,
		})
	})

	mux.HandleFunc("GET /api/inventory/expiring", func(w http.ResponseWriter, r *http.Request) {
		daysStr := r.URL.Query().Get("days")
		days := 7
		if daysStr != "" {
			if d, err := strconv.Atoi(daysStr); err == nil {
				days = d
			}
		}
		today := time.Now().Format("2006-01-02")
		cutoff := time.Now().AddDate(0, 0, days).Format("2006-01-02")
		rows, err := db.Query(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode FROM inventory WHERE expiration_date != '' AND expiration_date >= ? AND expiration_date <= ?`, today, cutoff)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		WriteJSON(w, http.StatusOK, scanInventoryRows(rows))
	})

	mux.HandleFunc("GET /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		row := db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode FROM inventory WHERE id=?`, id)
		item, err := scanInventoryRow(row)
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

	mux.HandleFunc("GET /api/inventory/", func(w http.ResponseWriter, r *http.Request) {
		q := `SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode FROM inventory WHERE 1=1`
		args := []any{}
		if name := r.URL.Query().Get("name"); name != "" {
			q += ` AND name LIKE ?`
			args = append(args, "%"+name+"%")
		}
		if loc := r.URL.Query().Get("location"); loc != "" {
			q += ` AND location=?`
			args = append(args, loc)
		}
		rows, err := db.Query(q, args...)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		WriteJSON(w, http.StatusOK, scanInventoryRows(rows))
	})

	mux.HandleFunc("PATCH /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
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
		allowed := []string{"name", "quantity", "unit", "location", "expiration_date", "low_threshold", "barcode"}
		for _, field := range allowed {
			if val, ok := patch[field]; ok {
				db.Exec(`UPDATE inventory SET `+field+`=? WHERE id=?`, val, id)
			}
		}
		row := db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode FROM inventory WHERE id=?`, id)
		item, err := scanInventoryRow(row)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		WriteJSON(w, http.StatusOK, item)
	})

	mux.HandleFunc("DELETE /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		res, _ := db.Exec(`DELETE FROM inventory WHERE id=?`, id)
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func scanInventoryRow(row *sql.Row) (map[string]any, error) {
	var id int64
	var name, unit, location, expDate, barcode string
	var qty, threshold float64
	err := row.Scan(&id, &name, &qty, &unit, &location, &expDate, &threshold, &barcode)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "name": name, "quantity": qty, "unit": unit,
		"location": location, "expiration_date": expDate,
		"low_threshold": threshold, "barcode": barcode,
	}, nil
}

func scanInventoryRows(rows *sql.Rows) []map[string]any {
	var items []map[string]any
	for rows.Next() {
		var id int64
		var name, unit, location, expDate, barcode string
		var qty, threshold float64
		if err := rows.Scan(&id, &name, &qty, &unit, &location, &expDate, &threshold, &barcode); err == nil {
			items = append(items, map[string]any{
				"id": id, "name": name, "quantity": qty, "unit": unit,
				"location": location, "expiration_date": expDate,
				"low_threshold": threshold, "barcode": barcode,
			})
		}
	}
	if items == nil {
		return []map[string]any{}
	}
	return items
}

// pathIDFromPattern reads a named path parameter from Go 1.22 ServeMux patterns.
func pathIDFromPattern(r *http.Request, param string) (int64, bool) {
	val := r.PathValue(param)
	id, err := strconv.ParseInt(val, 10, 64)
	return id, err == nil
}
