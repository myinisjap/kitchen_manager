package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kitchen_manager/units"
)

func RegisterInventory(mux *http.ServeMux, db *sql.DB, hub ...*Hub) {
	var wsHub *Hub
	if len(hub) > 0 {
		wsHub = hub[0]
	}
	broadcastInventory := func() {
		if wsHub != nil {
			wsHub.Broadcast("inventory_updated")
		}
	}
	mux.HandleFunc("GET /api/units", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string][]string{
			"mass":   {"g", "kg", "oz", "lb"},
			"volume": {"ml", "L", "cup", "tbsp", "tsp"},
			"count":  {"piece", "clove", "can", "jar", "bunch"},
		})
	})

	mux.HandleFunc("POST /api/inventory/", func(w http.ResponseWriter, r *http.Request) {
		var item struct {
			Name             string  `json:"name"`
			Quantity         float64 `json:"quantity"`
			Unit             string  `json:"unit"`
			PreferredUnit    string  `json:"preferred_unit"`
			Location         string  `json:"location"`
			ExpirationDate   string  `json:"expiration_date"`
			LowThreshold     float64 `json:"low_threshold"`
			Barcode          string  `json:"barcode"`
			UnitCostCents    int64   `json:"unit_cost_cents"`
			QuantityPerScan  float64 `json:"quantity_per_scan"`
		}
		if err := ReadJSON(r, &item); err != nil || item.Name == "" {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if item.Quantity < 0 {
			WriteError(w, http.StatusBadRequest, "quantity must be non-negative")
			return
		}
		if item.LowThreshold < 0 {
			WriteError(w, http.StatusBadRequest, "low_threshold must be non-negative")
			return
		}
		if item.LowThreshold == 0 {
			item.LowThreshold = 1
		}
		if item.ExpirationDate != "" {
			if _, err := time.Parse("2006-01-02", item.ExpirationDate); err != nil {
				WriteError(w, http.StatusBadRequest, "invalid expiration_date format, expected YYYY-MM-DD")
				return
			}
		}
		if item.Unit != "" && !units.IsValid(units.Unit(item.Unit)) {
			WriteError(w, http.StatusBadRequest, "invalid unit; valid units: g, kg, oz, lb, ml, L, cup, tbsp, tsp, piece, clove, can, jar, bunch")
			return
		}
		if item.PreferredUnit != "" {
			if !units.IsValid(units.Unit(item.PreferredUnit)) {
				WriteError(w, http.StatusBadRequest, "invalid preferred_unit; valid units: g, kg, oz, lb, ml, L, cup, tbsp, tsp, piece, clove, can, jar, bunch")
				return
			}
			if item.Unit != "" && units.BaseDimension(units.Unit(item.Unit)) != units.BaseDimension(units.Unit(item.PreferredUnit)) {
				WriteError(w, http.StatusBadRequest, "preferred_unit must be in the same dimension as unit")
				return
			}
		}
		if item.QuantityPerScan <= 0 {
			item.QuantityPerScan = 1
		}
		item.Name = strings.ToLower(strings.TrimSpace(item.Name))
		res, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			item.Name, item.Quantity, item.Unit, item.Location, item.ExpirationDate, item.LowThreshold, item.Barcode, item.PreferredUnit, item.UnitCostCents, item.QuantityPerScan)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		zeroQty := 0.0
		newQty := item.Quantity
		_ = LogHistory(db, nil, LogHistoryParams{
			InventoryID:   id,
			ItemName:      item.Name,
			ChangeType:    "add",
			QuantityBefore: &zeroQty,
			QuantityAfter:  &newQty,
			Unit:          item.Unit,
			Source:        "manual",
		})
		WriteJSON(w, http.StatusCreated, map[string]any{
			"id": id, "name": item.Name, "quantity": item.Quantity,
			"unit": item.Unit, "preferred_unit": item.PreferredUnit,
			"location": item.Location, "expiration_date": item.ExpirationDate,
			"low_threshold": item.LowThreshold, "barcode": item.Barcode,
			"unit_cost_cents": item.UnitCostCents, "quantity_per_scan": item.QuantityPerScan,
		})
		broadcastInventory()
	})

	mux.HandleFunc("GET /api/inventory/expiring", func(w http.ResponseWriter, r *http.Request) {
		daysStr := r.URL.Query().Get("days")
		days := 7
		if daysStr != "" {
			if d, err := strconv.Atoi(daysStr); err == nil {
				days = d
			}
		}
		cutoff := time.Now().AddDate(0, 0, days).Format("2006-01-02")
		rows, err := db.Query(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE expiration_date != '' AND expiration_date <= ?`, cutoff)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		items, err := scanInventoryRows(rows)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, items)
	})

	mux.HandleFunc("GET /api/inventory/suggestions", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		var rows *sql.Rows
		var err error
		if q == "" {
			rows, err = db.Query(`SELECT name, MAX(unit), MAX(preferred_unit), MAX(location), MAX(low_threshold) FROM inventory GROUP BY name ORDER BY name LIMIT 10`)
		} else {
			rows, err = db.Query(`SELECT name, MAX(unit), MAX(preferred_unit), MAX(location), MAX(low_threshold) FROM inventory WHERE name LIKE ? GROUP BY name ORDER BY name LIMIT 10`, q+"%")
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		var suggestions []map[string]any
		for rows.Next() {
			var name, unit, preferredUnit, location string
			var lowThreshold float64
			if err := rows.Scan(&name, &unit, &preferredUnit, &location, &lowThreshold); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			suggestions = append(suggestions, map[string]any{
				"name":           name,
				"unit":           unit,
				"preferred_unit": preferredUnit,
				"location":       location,
				"low_threshold":  lowThreshold,
			})
		}
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if suggestions == nil {
			suggestions = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, suggestions)
	})

	mux.HandleFunc("GET /api/inventory/barcode/{code}", func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		if code == "" {
			WriteError(w, http.StatusBadRequest, "missing barcode")
			return
		}
		row := db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE barcode=?`, code)
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

	mux.HandleFunc("GET /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		row := db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, id)
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
		q := `SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE 1=1`
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
		items, err := scanInventoryRows(rows)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, items)
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
		if expDate, ok := patch["expiration_date"]; ok {
			if expStr, ok := expDate.(string); ok && expStr != "" {
				if _, err := time.Parse("2006-01-02", expStr); err != nil {
					WriteError(w, http.StatusBadRequest, "invalid expiration_date format, expected YYYY-MM-DD")
					return
				}
			}
		}
		if qty, ok := patch["quantity"]; ok {
			if q, ok := qty.(float64); ok && q < 0 {
				WriteError(w, http.StatusBadRequest, "quantity cannot be negative")
				return
			}
		}
		if thresh, ok := patch["low_threshold"]; ok {
			if t, ok := thresh.(float64); ok && t < 0 {
				WriteError(w, http.StatusBadRequest, "low_threshold cannot be negative")
				return
			}
		}
		if u, ok := patch["unit"]; ok {
			if uStr, ok := u.(string); ok && uStr != "" && !units.IsValid(units.Unit(uStr)) {
				WriteError(w, http.StatusBadRequest, "invalid unit; valid units: g, kg, oz, lb, ml, L, cup, tbsp, tsp, piece, clove, can, jar, bunch")
				return
			}
		}
		if pu, ok := patch["preferred_unit"]; ok {
			if puStr, ok := pu.(string); ok && puStr != "" {
				if !units.IsValid(units.Unit(puStr)) {
					WriteError(w, http.StatusBadRequest, "invalid preferred_unit; valid units: g, kg, oz, lb, ml, L, cup, tbsp, tsp, piece, clove, can, jar, bunch")
					return
				}
				if u, ok := patch["unit"]; ok {
					if uStr, ok := u.(string); ok && uStr != "" {
						if units.BaseDimension(units.Unit(uStr)) != units.BaseDimension(units.Unit(puStr)) {
							WriteError(w, http.StatusBadRequest, "preferred_unit must be in the same dimension as unit")
							return
						}
					}
				}
			}
		}
		// Fetch current quantity before patching (for history)
		var qtyBefore float64
		var nameBefore, unitBefore string
		_ = db.QueryRow(`SELECT quantity, name, unit FROM inventory WHERE id=?`, id).Scan(&qtyBefore, &nameBefore, &unitBefore)

		if n, ok := patch["name"]; ok {
			if nStr, ok := n.(string); ok {
				patch["name"] = strings.ToLower(strings.TrimSpace(nStr))
			}
		}
		allowed := []string{"name", "quantity", "unit", "location", "expiration_date", "low_threshold", "barcode", "preferred_unit", "unit_cost_cents", "quantity_per_scan"}
		for _, field := range allowed {
			if val, ok := patch[field]; ok {
				if _, err := db.Exec(`UPDATE inventory SET `+field+`=? WHERE id=?`, val, id); err != nil {
					WriteError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
		}
		row := db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, id)
		item, err := scanInventoryRow(row)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		} else if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Log the change
		source := r.URL.Query().Get("source")
		if source == "" {
			source = "manual"
		}
		if _, hasQty := patch["quantity"]; hasQty {
			qtyAfter := item["quantity"].(float64)
			changeType := "edit"
			if qtyAfter > qtyBefore {
				changeType = "add"
			} else if qtyAfter < qtyBefore {
				changeType = "deduct"
			}
			_ = LogHistory(db, nil, LogHistoryParams{
				InventoryID:    id,
				ItemName:       nameBefore,
				ChangeType:     changeType,
				QuantityBefore: &qtyBefore,
				QuantityAfter:  &qtyAfter,
				Unit:           unitBefore,
				Source:         source,
			})
		}
		WriteJSON(w, http.StatusOK, item)
		broadcastInventory()
	})

	mux.HandleFunc("DELETE /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		// Fetch before deleting for history
		var itemName, itemUnit string
		var itemQty float64
		_ = db.QueryRow(`SELECT name, quantity, unit FROM inventory WHERE id=?`, id).Scan(&itemName, &itemQty, &itemUnit)
		res, _ := db.Exec(`DELETE FROM inventory WHERE id=?`, id)
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		_ = LogHistory(db, nil, LogHistoryParams{
			InventoryID:    id,
			ItemName:       itemName,
			ChangeType:     "delete",
			QuantityBefore: &itemQty,
			QuantityAfter:  nil,
			Unit:           itemUnit,
			Source:         "manual",
		})
		w.WriteHeader(http.StatusNoContent)
		broadcastInventory()
	})
}

func scanInventoryRow(row *sql.Row) (map[string]any, error) {
	var id int64
	var name, unit, location, expDate, barcode, preferredUnit string
	var qty, threshold, qtyPerScan float64
	var unitCostCents int64
	err := row.Scan(&id, &name, &qty, &unit, &location, &expDate, &threshold, &barcode, &preferredUnit, &unitCostCents, &qtyPerScan)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "name": name, "quantity": qty, "unit": unit,
		"preferred_unit": preferredUnit, "location": location,
		"expiration_date": expDate, "low_threshold": threshold, "barcode": barcode,
		"unit_cost_cents": unitCostCents, "quantity_per_scan": qtyPerScan,
	}, nil
}

func scanInventoryRows(rows *sql.Rows) ([]map[string]any, error) {
	var items []map[string]any
	for rows.Next() {
		var id int64
		var name, unit, location, expDate, barcode, preferredUnit string
		var qty, threshold, qtyPerScan float64
		var unitCostCents int64
		if err := rows.Scan(&id, &name, &qty, &unit, &location, &expDate, &threshold, &barcode, &preferredUnit, &unitCostCents, &qtyPerScan); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "quantity": qty, "unit": unit,
			"preferred_unit": preferredUnit, "location": location,
			"expiration_date": expDate, "low_threshold": threshold, "barcode": barcode,
			"unit_cost_cents": unitCostCents, "quantity_per_scan": qtyPerScan,
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

// pathIDFromPattern reads a named path parameter from Go 1.22 ServeMux patterns.
func pathIDFromPattern(r *http.Request, param string) (int64, bool) {
	val := r.PathValue(param)
	id, err := strconv.ParseInt(val, 10, 64)
	return id, err == nil
}
