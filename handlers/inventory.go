package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"kitchen_manager/internal/auth"
	"kitchen_manager/services"
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
			Name            string  `json:"name"`
			Quantity        float64 `json:"quantity"`
			Unit            string  `json:"unit"`
			PreferredUnit   string  `json:"preferred_unit"`
			Location        string  `json:"location"`
			ExpirationDate  string  `json:"expiration_date"`
			LowThreshold    float64 `json:"low_threshold"`
			Barcode         string  `json:"barcode"`
			UnitCostCents   int64   `json:"unit_cost_cents"`
			QuantityPerScan float64 `json:"quantity_per_scan"`
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
		if item.QuantityPerScan < 0 {
			item.QuantityPerScan = 0
		}
		item.Name = strings.ToLower(strings.TrimSpace(item.Name))

		// Check for an existing item with identical identifying fields; if found, add quantity instead of inserting.
		// Only merge when barcode is non-empty to avoid incorrectly merging unrelated items with no barcode.
		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback() // no-op after Commit
		changedBy := auth.EmailFromContext(r.Context())

		var existingID int64
		if item.Barcode != "" {
			err = tx.QueryRow(
				`SELECT id FROM inventory WHERE name=? AND unit=? AND location=? AND barcode=? AND expiration_date=? LIMIT 1`,
				item.Name, item.Unit, item.Location, item.Barcode, item.ExpirationDate,
			).Scan(&existingID)
		} else {
			err = sql.ErrNoRows
		}
		if err != nil && err != sql.ErrNoRows {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if err == nil {
			// Duplicate found — atomically add quantity to the existing item.
			before, after, err := addStock(tx, existingID, item.Quantity)
			if err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			_ = LogHistory(db, tx, LogHistoryParams{
				InventoryID:    existingID,
				ItemName:       item.Name,
				ChangeType:     "add",
				QuantityBefore: &before,
				QuantityAfter:  &after,
				ChangedBy:      changedBy,
				Unit:           item.Unit,
				Source:         "manual",
			})
			if item.LowThreshold > 0 {
				if err := setProductPar(tx, item.Name, item.LowThreshold); err != nil {
					WriteError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			updated, err := scanInventoryRow(tx.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, existingID))
			if err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if err := tx.Commit(); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			WriteJSON(w, http.StatusOK, updated)
			broadcastInventory()
			return
		}

		// No primary barcode match — check if barcode is a SKU alias.
		if item.Barcode != "" {
			var skuInvID int64
			skuErr := tx.QueryRow(`SELECT inventory_id FROM inventory_skus WHERE barcode=?`, item.Barcode).Scan(&skuInvID)
			if skuErr == nil {
				var parentUnit, parentName string
				if e := tx.QueryRow(`SELECT unit, name FROM inventory WHERE id=?`, skuInvID).Scan(&parentUnit, &parentName); e != nil {
					WriteError(w, http.StatusInternalServerError, e.Error())
					return
				}
				// Convert incoming quantity to parent's unit if possible.
				addQty := item.Quantity
				if item.Unit != "" && parentUnit != "" && item.Unit != parentUnit {
					if converted, convErr := units.Convert(addQty, units.Unit(item.Unit), units.Unit(parentUnit)); convErr == nil {
						addQty = converted
					}
				}
				before, after, e := addStock(tx, skuInvID, addQty)
				if e != nil {
					WriteError(w, http.StatusInternalServerError, e.Error())
					return
				}
				_ = LogHistory(db, tx, LogHistoryParams{
					InventoryID:    skuInvID,
					ItemName:       parentName,
					ChangeType:     "add",
					QuantityBefore: &before,
					QuantityAfter:  &after,
					ChangedBy:      changedBy,
					Unit:           parentUnit,
					Source:         "receipt_merge",
				})
				if item.LowThreshold > 0 {
					if e := setProductPar(tx, parentName, item.LowThreshold); e != nil {
						WriteError(w, http.StatusInternalServerError, e.Error())
						return
					}
				}
				updated, e := scanInventoryRow(tx.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, skuInvID))
				if e != nil {
					WriteError(w, http.StatusInternalServerError, e.Error())
					return
				}
				if e := tx.Commit(); e != nil {
					WriteError(w, http.StatusInternalServerError, e.Error())
					return
				}
				WriteJSON(w, http.StatusOK, updated)
				broadcastInventory()
				return
			} else if skuErr != sql.ErrNoRows {
				WriteError(w, http.StatusInternalServerError, skuErr.Error())
				return
			}
		}

		if item.LowThreshold == 0 {
			// Unset: inherit the product's existing par level (0 if none).
			if err := tx.QueryRow(`SELECT COALESCE(MAX(low_threshold),0) FROM inventory WHERE name=?`, item.Name).Scan(&item.LowThreshold); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		res, err := tx.Exec(`INSERT INTO inventory (name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			item.Name, item.Quantity, item.Unit, item.Location, item.ExpirationDate, item.LowThreshold, item.Barcode, item.PreferredUnit, item.UnitCostCents, item.QuantityPerScan)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		if item.LowThreshold > 0 {
			if err := setProductPar(tx, item.Name, item.LowThreshold); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		zeroQty := 0.0
		newQty := item.Quantity
		_ = LogHistory(db, tx, LogHistoryParams{
			InventoryID:    id,
			ItemName:       item.Name,
			ChangeType:     "add",
			QuantityBefore: &zeroQty,
			QuantityAfter:  &newQty,
			Unit:           item.Unit,
			Source:         "manual",
			ChangedBy:      changedBy,
		})
		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
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
		_, cutoff := expiryDaysAndCutoff(r, db)
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
			rows, err = db.Query(`SELECT i.name, MAX(i.unit) AS unit, MAX(i.preferred_unit) AS preferred_unit, MAX(i.low_threshold) AS low_threshold, COALESCE((SELECT inv.location FROM inventory inv JOIN inventory_history h ON h.inventory_id = inv.id WHERE inv.name = i.name ORDER BY h.changed_at DESC LIMIT 1), MAX(i.location)) AS location FROM inventory i GROUP BY i.name ORDER BY i.name LIMIT 10`)
		} else {
			rows, err = db.Query(`SELECT i.name, MAX(i.unit) AS unit, MAX(i.preferred_unit) AS preferred_unit, MAX(i.low_threshold) AS low_threshold, COALESCE((SELECT inv.location FROM inventory inv JOIN inventory_history h ON h.inventory_id = inv.id WHERE inv.name = i.name ORDER BY h.changed_at DESC LIMIT 1), MAX(i.location)) AS location FROM inventory i WHERE i.name LIKE ? GROUP BY i.name ORDER BY i.name LIMIT 10`, q+"%")
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		var suggestions []map[string]any
		for rows.Next() {
			var name, unit, preferredUnit string
			var lowThreshold float64
			var location sql.NullString
			if err := rows.Scan(&name, &unit, &preferredUnit, &lowThreshold, &location); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			loc := ""
			if location.Valid {
				loc = location.String
			}
			suggestions = append(suggestions, map[string]any{
				"name":           name,
				"unit":           unit,
				"preferred_unit": preferredUnit,
				"location":       loc,
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
			// Fallback: check SKU alias table
			var skuID, skuInventoryID int64
			var skuQtyPerScan float64
			skuErr := db.QueryRow(`SELECT id, inventory_id, quantity_per_scan FROM inventory_skus WHERE barcode=?`, code).Scan(&skuID, &skuInventoryID, &skuQtyPerScan)
			if skuErr == sql.ErrNoRows {
				WriteError(w, http.StatusNotFound, "not found")
				return
			}
			if skuErr != nil {
				WriteError(w, http.StatusInternalServerError, skuErr.Error())
				return
			}
			row2 := db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, skuInventoryID)
			item2, err2 := scanInventoryRow(row2)
			if err2 != nil {
				WriteError(w, http.StatusInternalServerError, err2.Error())
				return
			}
			item2["quantity_per_scan"] = skuQtyPerScan
			item2["sku_id"] = skuID
			item2["matched_barcode"] = code
			WriteJSON(w, http.StatusOK, item2)
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, item)
	})

	mux.HandleFunc("GET /api/inventory/similar", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if name == "" {
			WriteJSON(w, http.StatusOK, []map[string]any{})
			return
		}
		// Build per-word LIKE conditions so "creamy peanut butter" matches "peanut butter"
		words := strings.Fields(strings.ToLower(name))
		if len(words) == 0 {
			WriteJSON(w, http.StatusOK, []map[string]any{})
			return
		}
		conds := make([]string, len(words))
		args := make([]any, len(words))
		for i, w := range words {
			conds[i] = "name LIKE ?"
			args[i] = "%" + w + "%"
		}
		q := `SELECT id, name, quantity, unit, preferred_unit, location FROM inventory WHERE ` + strings.Join(conds, " AND ") + ` LIMIT 3`
		rows, err := db.Query(q, args...)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		var candidates []map[string]any
		for rows.Next() {
			var id int64
			var n, unit, preferredUnit, location string
			var qty float64
			if err := rows.Scan(&id, &n, &qty, &unit, &preferredUnit, &location); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			candidates = append(candidates, map[string]any{
				"id": id, "name": n, "quantity": qty,
				"unit": unit, "preferred_unit": preferredUnit, "location": location,
			})
		}
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if candidates == nil {
			candidates = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, candidates)
	})

	mux.HandleFunc("GET /api/inventory/grouped", func(w http.ResponseWriter, r *http.Request) {
		locFilter := r.URL.Query().Get("location")
		nameFilter := r.URL.Query().Get("name")

		q := `SELECT name, unit, MAX(preferred_unit) AS preferred_unit, MAX(low_threshold) AS low_threshold, SUM(quantity) AS total_quantity, json_group_array(json_object('id', id, 'location', location, 'quantity', quantity, 'expiration_date', expiration_date, 'barcode', barcode)) AS locations_json FROM inventory WHERE 1=1`
		args := []any{}
		if locFilter != "" {
			q += ` AND location=?`
			args = append(args, locFilter)
		}
		if nameFilter != "" {
			q += ` AND name LIKE ?`
			args = append(args, "%"+nameFilter+"%")
		}
		q += ` GROUP BY name, unit ORDER BY name`

		rows, err := db.Query(q, args...)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		type locationEntry struct {
			ID             int64   `json:"id"`
			Location       string  `json:"location"`
			Quantity       float64 `json:"quantity"`
			ExpirationDate string  `json:"expiration_date"`
			Barcode        string  `json:"barcode"`
		}

		type groupedItem struct {
			Name                  string          `json:"name"`
			Unit                  string          `json:"unit"`
			PreferredUnit         string          `json:"preferred_unit"`
			TotalQuantity         float64         `json:"total_quantity"`
			LowThreshold          float64         `json:"low_threshold"`
			ParLevel              float64         `json:"par_level"`
			ParUnit               string          `json:"par_unit"`
			ProductTotal          float64         `json:"product_total"`
			BelowPar              bool            `json:"below_par"`
			Locations             []locationEntry `json:"locations"`
			RecommendedLocationID int64           `json:"recommended_location_id"`
		}

		var results []groupedItem
		for rows.Next() {
			var name, unit, preferredUnit, locationsJSON string
			var totalQuantity, lowThreshold float64
			if err := rows.Scan(&name, &unit, &preferredUnit, &lowThreshold, &totalQuantity, &locationsJSON); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			var locs []locationEntry
			if err := json.Unmarshal([]byte(locationsJSON), &locs); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			// Sort by expiration ascending; empty/null expiration goes last
			sort.Slice(locs, func(i, j int) bool {
				ei, ej := locs[i].ExpirationDate, locs[j].ExpirationDate
				if ei == "" && ej == "" {
					return false
				}
				if ei == "" {
					return false
				}
				if ej == "" {
					return true
				}
				return ei < ej
			})
			var recommendedID int64
			if len(locs) > 0 {
				recommendedID = locs[0].ID
			}
			results = append(results, groupedItem{
				Name:                  name,
				Unit:                  unit,
				PreferredUnit:         preferredUnit,
				TotalQuantity:         totalQuantity,
				LowThreshold:          lowThreshold,
				Locations:             locs,
				RecommendedLocationID: recommendedID,
			})
		}
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows.Close()
		if results == nil {
			results = []groupedItem{}
		}
		levels, err := services.ProductLevels(db)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		byName := make(map[string]services.ProductLevel, len(levels))
		for _, l := range levels {
			byName[l.Name] = l
		}
		for i := range results {
			if l, ok := byName[results[i].Name]; ok {
				results[i].ParLevel = l.Par
				results[i].ParUnit = l.Unit
				results[i].ProductTotal = l.Total
				results[i].BelowPar = l.BelowPar()
			}
		}
		WriteJSON(w, http.StatusOK, results)
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
		var p inventoryPatch
		if err := ReadJSON(r, &p); err != nil {
			var ute *json.UnmarshalTypeError
			if errors.As(err, &ute) {
				WriteError(w, http.StatusBadRequest, "invalid type for field "+ute.Field)
				return
			}
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		const unitMsg = "valid units: g, kg, oz, lb, ml, L, cup, tbsp, tsp, piece, clove, can, jar, bunch"
		if p.ExpirationDate != nil && *p.ExpirationDate != "" {
			if _, err := time.Parse("2006-01-02", *p.ExpirationDate); err != nil {
				WriteError(w, http.StatusBadRequest, "invalid expiration_date format, expected YYYY-MM-DD")
				return
			}
		}
		if p.Quantity != nil && *p.Quantity < 0 {
			WriteError(w, http.StatusBadRequest, "quantity cannot be negative")
			return
		}
		if p.LowThreshold != nil && *p.LowThreshold < 0 {
			WriteError(w, http.StatusBadRequest, "low_threshold cannot be negative")
			return
		}
		if p.Unit != nil && *p.Unit != "" && !units.IsValid(units.Unit(*p.Unit)) {
			WriteError(w, http.StatusBadRequest, "invalid unit; "+unitMsg)
			return
		}
		if p.PreferredUnit != nil && *p.PreferredUnit != "" && !units.IsValid(units.Unit(*p.PreferredUnit)) {
			WriteError(w, http.StatusBadRequest, "invalid preferred_unit; "+unitMsg)
			return
		}
		if p.Name != nil {
			n := strings.ToLower(strings.TrimSpace(*p.Name))
			p.Name = &n
		}

		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback()

		var qtyBefore float64
		var nameBefore, unitBefore, prefBefore string
		err = tx.QueryRow(`SELECT quantity, name, unit, preferred_unit FROM inventory WHERE id=?`, id).
			Scan(&qtyBefore, &nameBefore, &unitBefore, &prefBefore)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		finalName, finalUnit, finalPref := nameBefore, unitBefore, prefBefore
		if p.Name != nil {
			finalName = *p.Name
		}
		if p.Unit != nil {
			finalUnit = *p.Unit
		}
		if p.PreferredUnit != nil {
			finalPref = *p.PreferredUnit
		}
		if p.PreferredUnit != nil && *p.PreferredUnit != "" && finalUnit != "" &&
			units.BaseDimension(units.Unit(finalUnit)) != units.BaseDimension(units.Unit(*p.PreferredUnit)) {
			WriteError(w, http.StatusBadRequest, "preferred_unit must be in the same dimension as unit")
			return
		}
		_ = finalPref

		var sets []string
		var args []any
		add := func(col string, v any) {
			sets = append(sets, col+"=?")
			args = append(args, v)
		}
		if p.Name != nil {
			add("name", *p.Name)
		}
		if p.Quantity != nil {
			add("quantity", *p.Quantity)
		}
		if p.Unit != nil {
			add("unit", *p.Unit)
		}
		if p.Location != nil {
			add("location", *p.Location)
		}
		if p.ExpirationDate != nil {
			add("expiration_date", *p.ExpirationDate)
		}
		if p.LowThreshold != nil {
			add("low_threshold", *p.LowThreshold)
		}
		if p.Barcode != nil {
			add("barcode", *p.Barcode)
		}
		if p.PreferredUnit != nil {
			add("preferred_unit", *p.PreferredUnit)
		}
		if p.UnitCostCents != nil {
			add("unit_cost_cents", *p.UnitCostCents)
		}
		if p.QuantityPerScan != nil {
			add("quantity_per_scan", *p.QuantityPerScan)
		}
		if len(sets) > 0 {
			args = append(args, id)
			if _, err := tx.Exec(`UPDATE inventory SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if p.LowThreshold != nil {
			if err := setProductPar(tx, finalName, *p.LowThreshold); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		} else if p.Name != nil {
			// Lot moved to another product: adopt that product's par, if it has other lots.
			var other sql.NullFloat64
			if err := tx.QueryRow(`SELECT MAX(low_threshold) FROM inventory WHERE name=? AND id<>?`, finalName, id).Scan(&other); err == nil && other.Valid {
				if _, err := tx.Exec(`UPDATE inventory SET low_threshold=? WHERE id=?`, other.Float64, id); err != nil {
					WriteError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
		}
		if p.Quantity != nil {
			source := r.URL.Query().Get("source")
			if source == "" {
				source = "manual"
			}
			qtyAfter := *p.Quantity
			changeType := "edit"
			if qtyAfter > qtyBefore {
				changeType = "add"
			} else if qtyAfter < qtyBefore {
				changeType = "deduct"
			}
			_ = LogHistory(db, tx, LogHistoryParams{
				InventoryID:    id,
				ItemName:       nameBefore,
				ChangeType:     changeType,
				QuantityBefore: &qtyBefore,
				QuantityAfter:  &qtyAfter,
				Unit:           unitBefore,
				Source:         source,
				ChangedBy:      auth.EmailFromContext(r.Context()),
			})
		}
		var survivor int64
		if p.Quantity != nil && *p.Quantity == 0 && qtyBefore > 0 {
			_, survivor, err = retireEmptyLots(tx, []int64{id}, finalName, finalUnit)
			if err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		item, err := scanInventoryRow(db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, id))
		if err == sql.ErrNoRows && survivor != 0 {
			item, err = scanInventoryRow(db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, survivor))
		}
		if err == sql.ErrNoRows {
			WriteJSON(w, http.StatusOK, map[string]any{"id": id, "quantity": 0})
			broadcastInventory()
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, item)
		broadcastInventory()
	})

	mux.HandleFunc("POST /api/inventory/{id}/deduct", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var body struct {
			Quantity  float64 `json:"quantity"`
			Unit      string  `json:"unit"`
			SingleLot bool    `json:"single_lot"`
		}
		if err := ReadJSON(r, &body); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if body.Quantity <= 0 {
			WriteError(w, http.StatusBadRequest, "quantity must be positive")
			return
		}
		if body.Unit != "" && !units.IsValid(units.Unit(body.Unit)) {
			WriteError(w, http.StatusBadRequest, "invalid unit")
			return
		}
		source := r.URL.Query().Get("source")
		if source == "" {
			source = "manual"
		}
		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback()
		dres, err := deductStock(db, tx, deductRequest{
			ItemID: id, Qty: body.Quantity, Unit: body.Unit, Source: source,
			ChangedBy: auth.EmailFromContext(r.Context()), SingleLot: body.SingleLot,
		})
		if err != nil {
			if errors.Is(err, errItemNotFound) {
				WriteError(w, http.StatusNotFound, err.Error())
			} else if errors.Is(err, errIncompatibleUnits) {
				WriteError(w, http.StatusUnprocessableEntity, err.Error())
			} else {
				WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := map[string]any{"id": id, "quantity": 0}
		if dres.ResultItemID != 0 {
			item, err := scanInventoryRow(db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, dres.ResultItemID))
			if err == nil {
				out = item
			} else if err != sql.ErrNoRows {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		out["deducted"] = dres.Deducted
		WriteJSON(w, http.StatusOK, out)
		broadcastInventory()
	})

	mux.HandleFunc("GET /api/skus/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		rows, err := db.Query(`SELECT id, inventory_id, barcode, quantity_per_scan FROM inventory_skus WHERE inventory_id=? ORDER BY id`, id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		var skus []map[string]any
		for rows.Next() {
			var skuID, invID int64
			var barcode string
			var qps float64
			if err := rows.Scan(&skuID, &invID, &barcode, &qps); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			skus = append(skus, map[string]any{"id": skuID, "inventory_id": invID, "barcode": barcode, "quantity_per_scan": qps})
		}
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if skus == nil {
			skus = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, skus)
	})

	mux.HandleFunc("POST /api/skus/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var body struct {
			Barcode         string  `json:"barcode"`
			QuantityPerScan float64 `json:"quantity_per_scan"`
			Quantity        float64 `json:"quantity"`
			Unit            string  `json:"unit"`
		}
		if err := ReadJSON(r, &body); err != nil || body.Barcode == "" {
			WriteError(w, http.StatusBadRequest, "barcode is required")
			return
		}

		// Fetch parent item
		var itemName, itemUnit, itemPreferredUnit string
		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback() // no-op after Commit
		err = tx.QueryRow(`SELECT name, unit, preferred_unit FROM inventory WHERE id=?`, id).Scan(&itemName, &itemUnit, &itemPreferredUnit)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "inventory item not found")
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Check barcode not already used in inventory.barcode or inventory_skus.barcode
		var collision int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM inventory WHERE barcode=?`, body.Barcode).Scan(&collision)
		if collision > 0 {
			WriteError(w, http.StatusConflict, "barcode already assigned to an inventory item")
			return
		}
		_ = tx.QueryRow(`SELECT COUNT(*) FROM inventory_skus WHERE barcode=?`, body.Barcode).Scan(&collision)
		if collision > 0 {
			WriteError(w, http.StatusConflict, "barcode already exists as a SKU alias")
			return
		}

		// Determine target unit for quantity addition
		targetUnit := itemPreferredUnit
		if targetUnit == "" {
			targetUnit = itemUnit
		}

		addQty := body.Quantity
		if body.Unit != "" && targetUnit != "" && body.Unit != targetUnit {
			fromDim := units.BaseDimension(units.Unit(body.Unit))
			toDim := units.BaseDimension(units.Unit(targetUnit))
			if fromDim == "" || toDim == "" || fromDim != toDim {
				WriteError(w, http.StatusBadRequest, "unit dimension mismatch: cannot add "+body.Unit+" to item tracked in "+targetUnit)
				return
			}
			converted, err := units.Convert(body.Quantity, units.Unit(body.Unit), units.Unit(targetUnit))
			if err != nil {
				WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			addQty = converted
		}

		// Insert SKU alias
		res, err := tx.Exec(`INSERT INTO inventory_skus (inventory_id, barcode, quantity_per_scan) VALUES (?,?,?)`, id, body.Barcode, body.QuantityPerScan)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		skuID, _ := res.LastInsertId()

		// Atomically add quantity to parent item
		before, after, err := addStock(tx, id, addQty)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if addQty != 0 {
			_ = LogHistory(db, tx, LogHistoryParams{
				InventoryID:    id,
				ItemName:       itemName,
				ChangeType:     "add",
				QuantityBefore: &before,
				QuantityAfter:  &after,
				Unit:           targetUnit,
				Source:         "barcode_merge",
				ChangedBy:      auth.EmailFromContext(r.Context()),
			})
		}

		item, err := scanInventoryRow(tx.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, id))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]any{
			"item": item,
			"sku":  map[string]any{"id": skuID, "inventory_id": id, "barcode": body.Barcode, "quantity_per_scan": body.QuantityPerScan},
		})
		broadcastInventory()
	})

	mux.HandleFunc("DELETE /api/skus/{sku_id}", func(w http.ResponseWriter, r *http.Request) {
		skuID, ok := pathIDFromPattern(r, "sku_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid sku_id")
			return
		}
		res, err := db.Exec(`DELETE FROM inventory_skus WHERE id=?`, skuID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "sku not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/inventory/{id}/merge-into/{target_id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		targetIDStr := r.PathValue("target_id")
		targetID, err := strconv.ParseInt(targetIDStr, 10, 64)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid target_id")
			return
		}
		if id == targetID {
			WriteError(w, http.StatusBadRequest, "source and target must be different items")
			return
		}

		// Optional body: override quantity/unit when dimensions differ
		var body struct {
			Quantity float64 `json:"quantity"`
			Unit     string  `json:"unit"`
		}
		_ = ReadJSON(r, &body)

		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback() // no-op after Commit

		// Fetch source
		var srcName, srcUnit, srcPreferred, srcBarcode string
		var srcQty float64
		err = tx.QueryRow(`SELECT name, quantity, unit, preferred_unit, barcode FROM inventory WHERE id=?`, id).
			Scan(&srcName, &srcQty, &srcUnit, &srcPreferred, &srcBarcode)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "source item not found")
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Fetch target
		var tgtName, tgtUnit, tgtPreferred string
		var tgtQty float64
		err = tx.QueryRow(`SELECT name, quantity, unit, preferred_unit FROM inventory WHERE id=?`, targetID).
			Scan(&tgtName, &tgtQty, &tgtUnit, &tgtPreferred)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "target item not found")
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		tgtEffUnit := tgtPreferred
		if tgtEffUnit == "" {
			tgtEffUnit = tgtUnit
		}

		// Determine quantity to add
		addQty := srcQty
		addUnit := srcUnit
		if body.Unit != "" {
			// Caller supplied override (unit conflict resolution)
			addQty = body.Quantity
			addUnit = body.Unit
		}

		if addUnit != "" && tgtEffUnit != "" && addUnit != tgtEffUnit {
			fromDim := units.BaseDimension(units.Unit(addUnit))
			toDim := units.BaseDimension(units.Unit(tgtEffUnit))
			if fromDim == "" || toDim == "" || fromDim != toDim {
				WriteError(w, http.StatusBadRequest, "unit dimension mismatch: cannot merge "+addUnit+" into item tracked in "+tgtEffUnit)
				return
			}
			converted, err := units.Convert(addQty, units.Unit(addUnit), units.Unit(tgtEffUnit))
			if err != nil {
				WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			addQty = converted
			addUnit = tgtEffUnit
		}

		// Re-link source's SKU aliases to target (skip any that would conflict)
		if _, err := tx.Exec(`UPDATE OR IGNORE inventory_skus SET inventory_id=? WHERE inventory_id=?`, targetID, id); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Migrate source's primary barcode as a SKU alias on target (if non-empty and not conflicting)
		if srcBarcode != "" {
			var collision int
			_ = tx.QueryRow(`SELECT COUNT(*) FROM inventory WHERE barcode=? AND id!=?`, srcBarcode, id).Scan(&collision)
			if collision == 0 {
				var skuCollision int
				_ = tx.QueryRow(`SELECT COUNT(*) FROM inventory_skus WHERE barcode=?`, srcBarcode).Scan(&skuCollision)
				if skuCollision == 0 {
					qps := srcQty
					if qps <= 0 {
						qps = 1
					}
					if _, err := tx.Exec(`INSERT OR IGNORE INTO inventory_skus (inventory_id, barcode, quantity_per_scan) VALUES (?,?,?)`, targetID, srcBarcode, qps); err != nil {
						WriteError(w, http.StatusInternalServerError, err.Error())
						return
					}
				}
			}
		}

		// Add quantity to target
		tgtBefore, newTgtQty, err := addStock(tx, targetID, addQty)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Point recipes, shopping list rows and meal history at the target
		if err := relinkInventoryRefs(tx, id, &targetID); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Delete source
		if _, err := tx.Exec(`DELETE FROM inventory WHERE id=?`, id); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		_ = LogHistory(db, tx, LogHistoryParams{
			InventoryID:    targetID,
			ItemName:       tgtName,
			ChangeType:     "add",
			QuantityBefore: &tgtBefore,
			QuantityAfter:  &newTgtQty,
			Unit:           tgtEffUnit,
			Source:         "item_merge",
			ChangedBy:      auth.EmailFromContext(r.Context()),
		})

		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		row := db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, targetID)
		item, err := scanInventoryRow(row)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
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
		err := db.QueryRow(`SELECT name, quantity, unit FROM inventory WHERE id=?`, id).Scan(&itemName, &itemQty, &itemUnit)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Hand references to another row of the same item if there is one,
		// otherwise clear them, so the delete doesn't break foreign keys.
		var relinkTo *int64
		siblingID, ok, err := findSurvivingSibling(tx, itemName, itemUnit, []int64{id})
		if err != nil {
			tx.Rollback()
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if ok {
			relinkTo = &siblingID
			if err := moveBarcodes(tx, id, siblingID); err != nil {
				tx.Rollback()
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if err := relinkInventoryRefs(tx, id, relinkTo); err != nil {
			tx.Rollback()
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := tx.Exec(`DELETE FROM inventory WHERE id=?`, id); err != nil {
			tx.Rollback()
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
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
			ChangedBy:      auth.EmailFromContext(r.Context()),
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

// setProductPar sets the par level (low_threshold) on every lot of a product.
func setProductPar(ex interface {
	Exec(string, ...any) (sql.Result, error)
}, name string, par float64) error {
	_, err := ex.Exec(`UPDATE inventory SET low_threshold=? WHERE name=?`, par, name)
	return err
}

type inventoryPatch struct {
	Name            *string  `json:"name"`
	Quantity        *float64 `json:"quantity"`
	Unit            *string  `json:"unit"`
	Location        *string  `json:"location"`
	ExpirationDate  *string  `json:"expiration_date"`
	LowThreshold    *float64 `json:"low_threshold"`
	Barcode         *string  `json:"barcode"`
	PreferredUnit   *string  `json:"preferred_unit"`
	UnitCostCents   *int64   `json:"unit_cost_cents"`
	QuantityPerScan *float64 `json:"quantity_per_scan"`
}
