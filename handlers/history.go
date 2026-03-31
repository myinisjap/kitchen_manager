package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
)

// LogHistoryParams holds the parameters for recording an inventory change.
type LogHistoryParams struct {
	InventoryID    int64
	ItemName       string
	ChangeType     string // "add", "deduct", "edit", "delete"
	QuantityBefore *float64
	QuantityAfter  *float64
	Unit           string
	Source         string // "manual", "barcode_add", "barcode_remove", "meal_cooked", "threshold", etc.
	RecipeID       *int64
}

// LogHistory writes a single row to inventory_history inside an optional transaction.
// Pass tx=nil to use db directly.
func LogHistory(db *sql.DB, tx *sql.Tx, p LogHistoryParams) error {
	q := `INSERT INTO inventory_history (inventory_id, item_name, change_type, quantity_before, quantity_after, unit, source, recipe_id)
		  VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	var err error
	if tx != nil {
		_, err = tx.Exec(q, p.InventoryID, p.ItemName, p.ChangeType, p.QuantityBefore, p.QuantityAfter, p.Unit, p.Source, p.RecipeID)
	} else {
		_, err = db.Exec(q, p.InventoryID, p.ItemName, p.ChangeType, p.QuantityBefore, p.QuantityAfter, p.Unit, p.Source, p.RecipeID)
	}
	return err
}

// RegisterHistory registers the three read endpoints for inventory history.
func RegisterHistory(mux *http.ServeMux, db *sql.DB) {
	// GET /api/history - paginated activity log
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		limitStr := r.URL.Query().Get("limit")
		offsetStr := r.URL.Query().Get("offset")
		limit := 50
		offset := 0
		if limitStr != "" {
			if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 200 {
				limit = v
			}
		}
		if offsetStr != "" {
			if v, err := strconv.Atoi(offsetStr); err == nil && v >= 0 {
				offset = v
			}
		}

		var total int
		if err := db.QueryRow(`SELECT COUNT(*) FROM inventory_history`).Scan(&total); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		rows, err := db.Query(`
			SELECT id, inventory_id, item_name, changed_at, COALESCE(changed_by,''), change_type,
			       quantity_before, quantity_after, unit, COALESCE(source,''), recipe_id
			FROM inventory_history
			ORDER BY changed_at DESC
			LIMIT ? OFFSET ?`, limit, offset)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		var items []map[string]any
		for rows.Next() {
			var id, inventoryID int64
			var itemName, changedAt, changedBy, changeType, unit, source string
			var qBefore, qAfter sql.NullFloat64
			var recipeID sql.NullInt64
			if err := rows.Scan(&id, &inventoryID, &itemName, &changedAt, &changedBy, &changeType,
				&qBefore, &qAfter, &unit, &source, &recipeID); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			row := map[string]any{
				"id":           id,
				"inventory_id": inventoryID,
				"item_name":    itemName,
				"changed_at":   changedAt,
				"changed_by":   changedBy,
				"change_type":  changeType,
				"unit":         unit,
				"source":       source,
			}
			if qBefore.Valid {
				row["quantity_before"] = qBefore.Float64
			} else {
				row["quantity_before"] = nil
			}
			if qAfter.Valid {
				row["quantity_after"] = qAfter.Float64
			} else {
				row["quantity_after"] = nil
			}
			if recipeID.Valid {
				row["recipe_id"] = recipeID.Int64
			} else {
				row["recipe_id"] = nil
			}
			items = append(items, row)
		}
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"total": total, "rows": items})
	})

	// GET /api/history/item/{id} - history for a single inventory item
	mux.HandleFunc("GET /api/history/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		limitStr := r.URL.Query().Get("limit")
		limit := 50
		if limitStr != "" {
			if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 200 {
				limit = v
			}
		}

		rows, err := db.Query(`
			SELECT id, inventory_id, item_name, changed_at, COALESCE(changed_by,''), change_type,
			       quantity_before, quantity_after, unit, COALESCE(source,''), recipe_id
			FROM inventory_history
			WHERE inventory_id = ?
			ORDER BY changed_at DESC
			LIMIT ?`, id, limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		var items []map[string]any
		for rows.Next() {
			var hid, inventoryID int64
			var itemName, changedAt, changedBy, changeType, unit, source string
			var qBefore, qAfter sql.NullFloat64
			var recipeID sql.NullInt64
			if err := rows.Scan(&hid, &inventoryID, &itemName, &changedAt, &changedBy, &changeType,
				&qBefore, &qAfter, &unit, &source, &recipeID); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			row := map[string]any{
				"id":           hid,
				"inventory_id": inventoryID,
				"item_name":    itemName,
				"changed_at":   changedAt,
				"changed_by":   changedBy,
				"change_type":  changeType,
				"unit":         unit,
				"source":       source,
			}
			if qBefore.Valid {
				row["quantity_before"] = qBefore.Float64
			} else {
				row["quantity_before"] = nil
			}
			if qAfter.Valid {
				row["quantity_after"] = qAfter.Float64
			} else {
				row["quantity_after"] = nil
			}
			if recipeID.Valid {
				row["recipe_id"] = recipeID.Int64
			} else {
				row["recipe_id"] = nil
			}
			items = append(items, row)
		}
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, items)
	})

	// GET /api/history/stats - top items by change count
	mux.HandleFunc("GET /api/history/stats", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(`
			SELECT inventory_id, item_name, COUNT(*) as change_count,
			       MAX(changed_at) as last_changed
			FROM inventory_history
			GROUP BY inventory_id, item_name
			ORDER BY change_count DESC
			LIMIT 10`)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		var stats []map[string]any
		for rows.Next() {
			var inventoryID int64
			var itemName, lastChanged string
			var count int
			if err := rows.Scan(&inventoryID, &itemName, &count, &lastChanged); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			stats = append(stats, map[string]any{
				"inventory_id":  inventoryID,
				"item_name":     itemName,
				"change_count":  count,
				"last_changed":  lastChanged,
			})
		}
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if stats == nil {
			stats = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, stats)
	})
}
