package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"kitchen_manager/internal/auth"
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
			StorageUnitID   *int64  `json:"storage_unit_id"`
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
		if item.QuantityPerScan < 0 {
			item.QuantityPerScan = 0
		}
		item.Name = strings.ToLower(strings.TrimSpace(item.Name))

		// Check for an existing item with identical identifying fields; if found, add quantity instead of inserting.
		// Only merge when barcode is non-empty to avoid incorrectly merging unrelated items with no barcode.
		var existingID int64
		var existingQty float64
		var err error
		if item.Barcode != "" {
			err = db.QueryRow(
				`SELECT id, quantity FROM inventory WHERE name=? AND unit=? AND storage_unit_id IS ? AND barcode=? AND expiration_date=? LIMIT 1`,
				item.Name, item.Unit, item.StorageUnitID, item.Barcode, item.ExpirationDate,
			).Scan(&existingID, &existingQty)
		} else {
			err = sql.ErrNoRows
		}
		if err != nil && err != sql.ErrNoRows {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if err == nil {
			// Duplicate found — update quantity on the existing item.
			newQty := existingQty + item.Quantity
			if _, err := db.Exec(`UPDATE inventory SET quantity=? WHERE id=?`, newQty, existingID); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			_ = LogHistory(db, nil, LogHistoryParams{
				InventoryID:    existingID,
				ItemName:       item.Name,
				ChangeType:     "add",
				QuantityBefore: &existingQty,
				QuantityAfter:  &newQty,
				ChangedBy:      auth.EmailFromContext(r.Context()),
				Unit:           item.Unit,
				Source:         "manual",
			})
			row := db.QueryRow(inventorySelect+` WHERE i.id=?`, existingID)
			updated, err := scanInventoryRow(row)
			if err != nil {
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
			skuErr := db.QueryRow(`SELECT inventory_id FROM inventory_skus WHERE barcode=?`, item.Barcode).Scan(&skuInvID)
			if skuErr == nil {
				// Load parent item's current quantity and unit.
				var parentQty float64
				var parentUnit, parentName string
				if e := db.QueryRow(`SELECT quantity, unit, name FROM inventory WHERE id=?`, skuInvID).Scan(&parentQty, &parentUnit, &parentName); e != nil {
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
				newQty := parentQty + addQty
				if _, e := db.Exec(`UPDATE inventory SET quantity=? WHERE id=?`, newQty, skuInvID); e != nil {
					WriteError(w, http.StatusInternalServerError, e.Error())
					return
				}
				_ = LogHistory(db, nil, LogHistoryParams{
					InventoryID:    skuInvID,
					ItemName:       parentName,
					ChangeType:     "add",
					QuantityBefore: &parentQty,
					QuantityAfter:  &newQty,
					ChangedBy:      auth.EmailFromContext(r.Context()),
					Unit:           parentUnit,
					Source:         "receipt_merge",
				})
				row := db.QueryRow(inventorySelect+` WHERE i.id=?`, skuInvID)
				updated, e := scanInventoryRow(row)
				if e != nil {
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

		res, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,storage_unit_id,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			item.Name, item.Quantity, item.Unit, item.StorageUnitID, item.ExpirationDate, item.LowThreshold, item.Barcode, item.PreferredUnit, item.UnitCostCents, item.QuantityPerScan)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		zeroQty := 0.0
		newQty := item.Quantity
		_ = LogHistory(db, nil, LogHistoryParams{
			InventoryID:    id,
			ItemName:       item.Name,
			ChangeType:     "add",
			QuantityBefore: &zeroQty,
			QuantityAfter:  &newQty,
			Unit:           item.Unit,
			Source:         "manual",
			ChangedBy:      auth.EmailFromContext(r.Context()),
		})
		// Re-read through inventorySelect so the 201 body carries the same nested
		// storage_unit shape as every other item response.
		created, err := scanInventoryRow(db.QueryRow(inventorySelect+` WHERE i.id=?`, id))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, created)
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
		rows, err := db.Query(inventorySelect+` WHERE i.expiration_date != '' AND i.expiration_date <= ?`, cutoff)
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
		// The "most recently used storage unit for this item name" smart-default is
		// preserved from the old free-text version: the correlated subquery now keys
		// on storage_unit_id, falling back to MAX(storage_unit_id) when the item has
		// no history rows yet.
		const suggestionSelect = `SELECT i.name, MAX(i.unit) AS unit, MAX(i.preferred_unit) AS preferred_unit, MAX(i.low_threshold) AS low_threshold,
			COALESCE((SELECT inv.storage_unit_id FROM inventory inv JOIN inventory_history h ON h.inventory_id = inv.id WHERE inv.name = i.name ORDER BY h.changed_at DESC LIMIT 1), MAX(i.storage_unit_id)) AS storage_unit_id
			FROM inventory i`
		if q == "" {
			rows, err = db.Query(suggestionSelect + ` GROUP BY i.name ORDER BY i.name LIMIT 10`)
		} else {
			rows, err = db.Query(suggestionSelect+` WHERE i.name LIKE ? GROUP BY i.name ORDER BY i.name LIMIT 10`, q+"%")
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
			var storageUnitID sql.NullInt64
			if err := rows.Scan(&name, &unit, &preferredUnit, &lowThreshold, &storageUnitID); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			suggestions = append(suggestions, map[string]any{
				"name":            name,
				"unit":            unit,
				"preferred_unit":  preferredUnit,
				"storage_unit_id": nullableID(storageUnitID),
				"low_threshold":   lowThreshold,
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

	// GET /api/inventory/locations (flat free-text autocomplete) is replaced by
	// GET /api/rooms, which returns the room -> storage unit hierarchy.

	mux.HandleFunc("GET /api/inventory/barcode/{code}", func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		if code == "" {
			WriteError(w, http.StatusBadRequest, "missing barcode")
			return
		}
		row := db.QueryRow(inventorySelect+` WHERE i.barcode=?`, code)
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
			row2 := db.QueryRow(inventorySelect+` WHERE i.id=?`, skuInventoryID)
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
			conds[i] = "i.name LIKE ?"
			args[i] = "%" + w + "%"
		}
		rows, err := db.Query(inventorySelect+` WHERE `+strings.Join(conds, " AND ")+` LIMIT 3`, args...)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		candidates, err := scanInventoryRows(rows)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, candidates)
	})

	mux.HandleFunc("GET /api/inventory/grouped", func(w http.ResponseWriter, r *http.Request) {
		unitFilter := r.URL.Query().Get("storage_unit_id")
		nameFilter := r.URL.Query().Get("name")

		q := `SELECT i.name, i.unit, MAX(i.preferred_unit) AS preferred_unit, MAX(i.low_threshold) AS low_threshold, SUM(i.quantity) AS total_quantity,
			json_group_array(json_object('id', i.id, 'storage_unit', CASE WHEN i.storage_unit_id IS NULL THEN json('null') ELSE json_object('id', u.id, 'name', u.name, 'room_id', r.id, 'room', r.name) END, 'quantity', i.quantity, 'expiration_date', i.expiration_date, 'barcode', i.barcode)) AS locations_json
			FROM inventory i
			LEFT JOIN storage_units u ON u.id = i.storage_unit_id
			LEFT JOIN rooms r ON r.id = u.room_id
			WHERE 1=1`
		args := []any{}
		if unitFilter == "__none__" {
			q += ` AND i.storage_unit_id IS NULL`
		} else if unitFilter != "" {
			id, err := strconv.ParseInt(unitFilter, 10, 64)
			if err != nil {
				WriteError(w, http.StatusBadRequest, "invalid storage_unit_id")
				return
			}
			q += ` AND i.storage_unit_id=?`
			args = append(args, id)
		}
		if nameFilter != "" {
			q += ` AND i.name LIKE ?`
			args = append(args, "%"+nameFilter+"%")
		}
		q += ` GROUP BY i.name, i.unit ORDER BY i.name`

		rows, err := db.Query(q, args...)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		type groupedStorageUnit struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			RoomID int64  `json:"room_id"`
			Room   string `json:"room"`
		}

		type locationEntry struct {
			ID             int64               `json:"id"`
			StorageUnit    *groupedStorageUnit `json:"storage_unit"`
			Quantity       float64             `json:"quantity"`
			ExpirationDate string              `json:"expiration_date"`
			Barcode        string              `json:"barcode"`
		}

		type groupedItem struct {
			Name                  string          `json:"name"`
			Unit                  string          `json:"unit"`
			PreferredUnit         string          `json:"preferred_unit"`
			TotalQuantity         float64         `json:"total_quantity"`
			LowThreshold          float64         `json:"low_threshold"`
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
		if results == nil {
			results = []groupedItem{}
		}
		WriteJSON(w, http.StatusOK, results)
	})

	mux.HandleFunc("GET /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		row := db.QueryRow(inventorySelect+` WHERE i.id=?`, id)
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
		q := inventorySelect + ` WHERE 1=1`
		args := []any{}
		if name := r.URL.Query().Get("name"); name != "" {
			q += ` AND i.name LIKE ?`
			args = append(args, "%"+name+"%")
		}
		if su := r.URL.Query().Get("storage_unit_id"); su != "" {
			if su == "__none__" {
				q += ` AND i.storage_unit_id IS NULL`
			} else {
				id, err := strconv.ParseInt(su, 10, 64)
				if err != nil {
					WriteError(w, http.StatusBadRequest, "invalid storage_unit_id")
					return
				}
				q += ` AND i.storage_unit_id=?`
				args = append(args, id)
			}
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
		newQtyVal, hasQty := patch["quantity"]
		newQty, isFloat := newQtyVal.(float64)
		isDeduction := hasQty && isFloat && newQty < qtyBefore

		if isDeduction {
			// Cascade deduction across all siblings (same name+unit) ordered by earliest expiration first.
			// Items with no expiration date are consumed last.
			deduct := qtyBefore - newQty

			type sibRow struct {
				id  int64
				qty float64
			}
			var sibs []sibRow

			// If location_id is provided and non-zero, bypass cascade and deduct only from that row.
			if locIDVal, ok := patch["location_id"]; ok {
				if locIDFloat, ok := locIDVal.(float64); ok && locIDFloat != 0 {
					locID := int64(locIDFloat)
					var s sibRow
					err := db.QueryRow(`SELECT id, quantity FROM inventory WHERE id=?`, locID).Scan(&s.id, &s.qty)
					if err == sql.ErrNoRows {
						WriteError(w, http.StatusNotFound, "location not found")
						return
					}
					if err != nil {
						WriteError(w, http.StatusInternalServerError, err.Error())
						return
					}
					sibs = []sibRow{s}
				}
			}

			if len(sibs) == 0 {
				sibRows, err := db.Query(
					`SELECT id, quantity FROM inventory WHERE name=? AND unit=?
					 ORDER BY CASE WHEN expiration_date='' THEN 1 ELSE 0 END ASC,
					          expiration_date ASC`,
					nameBefore, unitBefore,
				)
				if err != nil {
					WriteError(w, http.StatusInternalServerError, err.Error())
					return
				}
				for sibRows.Next() {
					var s sibRow
					if err := sibRows.Scan(&s.id, &s.qty); err != nil {
						sibRows.Close()
						WriteError(w, http.StatusInternalServerError, err.Error())
						return
					}
					sibs = append(sibs, s)
				}
				sibRows.Close()
				if err := sibRows.Err(); err != nil {
					WriteError(w, http.StatusInternalServerError, err.Error())
					return
				}
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

			var lastSibID int64
			for _, s := range sibs {
				if deduct <= 0 {
					break
				}
				if s.qty <= deduct {
					deduct -= s.qty
					zero := 0.0
					_ = LogHistory(db, tx, LogHistoryParams{
						InventoryID:    s.id,
						ItemName:       nameBefore,
						ChangeType:     "deduct",
						QuantityBefore: &s.qty,
						QuantityAfter:  &zero,
						Unit:           unitBefore,
						Source:         source,
						ChangedBy:      auth.EmailFromContext(r.Context()),
					})
					if _, err := tx.Exec(`UPDATE inventory SET quantity=0 WHERE id=?`, s.id); err != nil {
						tx.Rollback()
						WriteError(w, http.StatusInternalServerError, err.Error())
						return
					}
					lastSibID = s.id
				} else {
					remaining := s.qty - deduct
					deduct = 0
					lastSibID = s.id
					_ = LogHistory(db, tx, LogHistoryParams{
						InventoryID:    s.id,
						ItemName:       nameBefore,
						ChangeType:     "deduct",
						QuantityBefore: &s.qty,
						QuantityAfter:  &remaining,
						Unit:           unitBefore,
						Source:         source,
						ChangedBy:      auth.EmailFromContext(r.Context()),
					})
					if _, err := tx.Exec(`UPDATE inventory SET quantity=? WHERE id=?`, remaining, s.id); err != nil {
						tx.Rollback()
						WriteError(w, http.StatusInternalServerError, err.Error())
						return
					}
				}
			}

			if err := tx.Commit(); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}

			if lastSibID != 0 {
				row := db.QueryRow(inventorySelect+` WHERE i.id=?`, lastSibID)
				item, err := scanInventoryRow(row)
				if err != nil {
					WriteError(w, http.StatusInternalServerError, err.Error())
					return
				}
				WriteJSON(w, http.StatusOK, item)
			} else {
				// All sibling stock exhausted
				WriteJSON(w, http.StatusOK, map[string]any{"id": id, "quantity": 0})
			}
			broadcastInventory()
			return
		}

		// Non-deduction patch: apply fields directly.
		allowed := []string{"name", "quantity", "unit", "storage_unit_id", "expiration_date", "low_threshold", "barcode", "preferred_unit", "unit_cost_cents", "quantity_per_scan"}
		for _, field := range allowed {
			if val, ok := patch[field]; ok {
				if _, err := db.Exec(`UPDATE inventory SET `+field+`=? WHERE id=?`, val, id); err != nil {
					WriteError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
		}
		row := db.QueryRow(inventorySelect+` WHERE i.id=?`, id)
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
		if hasQty {
			qtyAfter := item["quantity"].(float64)
			changeType := "edit"
			if qtyAfter > qtyBefore {
				changeType = "add"
			}
			_ = LogHistory(db, nil, LogHistoryParams{
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
		WriteJSON(w, http.StatusOK, item)
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
			Barcode        string  `json:"barcode"`
			QuantityPerScan float64 `json:"quantity_per_scan"`
			Quantity       float64 `json:"quantity"`
			Unit           string  `json:"unit"`
		}
		if err := ReadJSON(r, &body); err != nil || body.Barcode == "" {
			WriteError(w, http.StatusBadRequest, "barcode is required")
			return
		}

		// Fetch parent item
		var itemName, itemUnit, itemPreferredUnit string
		var itemQty float64
		err := db.QueryRow(`SELECT name, quantity, unit, preferred_unit FROM inventory WHERE id=?`, id).Scan(&itemName, &itemQty, &itemUnit, &itemPreferredUnit)
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
		_ = db.QueryRow(`SELECT COUNT(*) FROM inventory WHERE barcode=?`, body.Barcode).Scan(&collision)
		if collision > 0 {
			WriteError(w, http.StatusConflict, "barcode already assigned to an inventory item")
			return
		}
		_ = db.QueryRow(`SELECT COUNT(*) FROM inventory_skus WHERE barcode=?`, body.Barcode).Scan(&collision)
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
		res, err := db.Exec(`INSERT INTO inventory_skus (inventory_id, barcode, quantity_per_scan) VALUES (?,?,?)`, id, body.Barcode, body.QuantityPerScan)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		skuID, _ := res.LastInsertId()

		// Add quantity to parent item
		newQty := itemQty + addQty
		if _, err := db.Exec(`UPDATE inventory SET quantity=? WHERE id=?`, newQty, id); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if addQty != 0 {
			_ = LogHistory(db, nil, LogHistoryParams{
				InventoryID:    id,
				ItemName:       itemName,
				ChangeType:     "add",
				QuantityBefore: &itemQty,
				QuantityAfter:  &newQty,
				Unit:           targetUnit,
				Source:         "barcode_merge",
				ChangedBy:      auth.EmailFromContext(r.Context()),
			})
		}

		row := db.QueryRow(inventorySelect+` WHERE i.id=?`, id)
		item, err := scanInventoryRow(row)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]any{
			"item": item,
			"sku":  map[string]any{"id": skuID, "inventory_id": id, "barcode": body.Barcode, "quantity_per_scan": body.QuantityPerScan},
		})
		broadcastInventory()
	})

	mux.HandleFunc("PATCH /api/skus/{sku_id}", func(w http.ResponseWriter, r *http.Request) {
		skuID, ok := pathIDFromPattern(r, "sku_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid sku_id")
			return
		}
		var body struct {
			Barcode        string  `json:"barcode"`
			QuantityPerScan float64 `json:"quantity_per_scan"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Barcode == "" {
			WriteError(w, http.StatusBadRequest, "barcode required")
			return
		}
		res, err := db.Exec(`UPDATE inventory_skus SET barcode=?, quantity_per_scan=? WHERE id=?`, body.Barcode, body.QuantityPerScan, skuID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "sku not found")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"id": skuID, "barcode": body.Barcode, "quantity_per_scan": body.QuantityPerScan})
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

		// Fetch source
		var srcName, srcUnit, srcPreferred, srcBarcode string
		var srcQty float64
		err = db.QueryRow(`SELECT name, quantity, unit, preferred_unit, barcode FROM inventory WHERE id=?`, id).
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
		err = db.QueryRow(`SELECT name, quantity, unit, preferred_unit FROM inventory WHERE id=?`, targetID).
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

		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Re-link source's SKU aliases to target (skip any that would conflict)
		if _, err := tx.Exec(`UPDATE OR IGNORE inventory_skus SET inventory_id=? WHERE inventory_id=?`, targetID, id); err != nil {
			tx.Rollback()
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
						tx.Rollback()
						WriteError(w, http.StatusInternalServerError, err.Error())
						return
					}
				}
			}
		}

		// Add quantity to target
		newTgtQty := tgtQty + addQty
		if _, err := tx.Exec(`UPDATE inventory SET quantity=? WHERE id=?`, newTgtQty, targetID); err != nil {
			tx.Rollback()
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Delete source
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
			InventoryID:    targetID,
			ItemName:       tgtName,
			ChangeType:     "add",
			QuantityBefore: &tgtQty,
			QuantityAfter:  &newTgtQty,
			Unit:           tgtEffUnit,
			Source:         "item_merge",
			ChangedBy:      auth.EmailFromContext(r.Context()),
		})

		row := db.QueryRow(inventorySelect+` WHERE i.id=?`, targetID)
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
			ChangedBy:      auth.EmailFromContext(r.Context()),
		})
		w.WriteHeader(http.StatusNoContent)
		broadcastInventory()
	})
}

// inventorySelect is the canonical inventory row projection. Every endpoint
// returning a full item uses it, so the column list and scan order stay in sync.
// It joins through to storage_units/rooms so responses carry human-readable
// names alongside storage_unit_id.
const inventorySelect = `SELECT i.id,i.name,i.quantity,i.unit,i.storage_unit_id,u.name,r.id,r.name,i.expiration_date,i.low_threshold,i.barcode,i.preferred_unit,i.unit_cost_cents,i.quantity_per_scan
	FROM inventory i
	LEFT JOIN storage_units u ON u.id = i.storage_unit_id
	LEFT JOIN rooms r ON r.id = u.room_id`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanInventoryRow reads one inventorySelect row. The storage unit is exposed as
// a nested object (null when the item is unfiled) so every endpoint returning an
// item hands the frontend the same shape.
func scanInventoryRow(s scanner) (map[string]any, error) {
	var id int64
	var name, unit, expDate, barcode, preferredUnit string
	var qty, threshold, qtyPerScan float64
	var unitCostCents int64
	var storageUnitID, roomID sql.NullInt64
	var storageUnitName, roomName sql.NullString
	err := s.Scan(&id, &name, &qty, &unit, &storageUnitID, &storageUnitName, &roomID, &roomName,
		&expDate, &threshold, &barcode, &preferredUnit, &unitCostCents, &qtyPerScan)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "name": name, "quantity": qty, "unit": unit,
		"preferred_unit":  preferredUnit,
		"storage_unit_id": nullableID(storageUnitID),
		"storage_unit":    storageUnitJSON(storageUnitID, storageUnitName, roomID, roomName),
		"expiration_date": expDate, "low_threshold": threshold, "barcode": barcode,
		"unit_cost_cents": unitCostCents, "quantity_per_scan": qtyPerScan,
	}, nil
}

func scanInventoryRows(rows *sql.Rows) ([]map[string]any, error) {
	items := []map[string]any{}
	for rows.Next() {
		item, err := scanInventoryRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func nullableID(id sql.NullInt64) any {
	if !id.Valid {
		return nil
	}
	return id.Int64
}

// storageUnitJSON is the one nested shape used everywhere an item's location is
// reported: {"id":1,"name":"Kitchen 1","room_id":2,"room":"Kitchen"}, or null.
func storageUnitJSON(unitID sql.NullInt64, unitName sql.NullString, roomID sql.NullInt64, roomName sql.NullString) any {
	if !unitID.Valid {
		return nil
	}
	return map[string]any{
		"id":      unitID.Int64,
		"name":    unitName.String,
		"room_id": roomID.Int64,
		"room":    roomName.String,
	}
}

// pathIDFromPattern reads a named path parameter from Go 1.22 ServeMux patterns.
func pathIDFromPattern(r *http.Request, param string) (int64, bool) {
	val := r.PathValue(param)
	id, err := strconv.ParseInt(val, 10, 64)
	return id, err == nil
}
