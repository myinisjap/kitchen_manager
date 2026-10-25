package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
)

// LogHistoryParams holds the parameters for recording an inventory change.
type LogHistoryParams struct {
	InventoryID    int64 // stock lot id
	ProductID      int64 // optional; looked up from the lot when 0
	ItemName       string
	ChangeType     string // "add", "deduct", "edit", "delete"
	QuantityBefore *float64
	QuantityAfter  *float64
	Unit           string
	Source         string // "manual", "barcode_add", "barcode_remove", "meal_cooked", "threshold", etc.
	RecipeID       *int64
	ChangedBy      string // email of the user making the change, empty if auth disabled
}

// LogHistory writes a single row to inventory_history inside an optional transaction.
// Pass tx=nil to use db directly.
func LogHistory(db *sql.DB, tx *sql.Tx, p LogHistoryParams) error {
	var changedBy any
	if p.ChangedBy != "" {
		changedBy = p.ChangedBy
	}
	var productID any
	if p.ProductID != 0 {
		productID = p.ProductID
	}
	q := `INSERT INTO inventory_history (inventory_id, product_id, item_name, change_type, quantity_before, quantity_after, unit, source, recipe_id, changed_by)
		  VALUES (?, COALESCE(?, (SELECT product_id FROM stock_lots WHERE id=?)), ?, ?, ?, ?, ?, ?, ?, ?)`
	var err error
	if tx != nil {
		_, err = tx.Exec(q, p.InventoryID, productID, p.InventoryID, p.ItemName, p.ChangeType, p.QuantityBefore, p.QuantityAfter, p.Unit, p.Source, p.RecipeID, changedBy)
	} else {
		_, err = db.Exec(q, p.InventoryID, productID, p.InventoryID, p.ItemName, p.ChangeType, p.QuantityBefore, p.QuantityAfter, p.Unit, p.Source, p.RecipeID, changedBy)
	}
	return err
}

const historyCols = `id, inventory_id, product_id, item_name, changed_at, COALESCE(changed_by,''), change_type,
			       quantity_before, quantity_after, unit, COALESCE(source,''), recipe_id`

// scanHistoryRows reads rows selected with historyCols into the API shape.
func scanHistoryRows(rows *sql.Rows) ([]map[string]any, error) {
	items := []map[string]any{}
	for rows.Next() {
		var id, inventoryID int64
		var itemName, changedAt, changedBy, changeType, unit, source string
		var qBefore, qAfter sql.NullFloat64
		var recipeID, productID sql.NullInt64
		if err := rows.Scan(&id, &inventoryID, &productID, &itemName, &changedAt, &changedBy, &changeType,
			&qBefore, &qAfter, &unit, &source, &recipeID); err != nil {
			return nil, err
		}
		row := map[string]any{
			"id":              id,
			"inventory_id":    inventoryID,
			"product_id":      nil,
			"item_name":       itemName,
			"changed_at":      changedAt,
			"changed_by":      changedBy,
			"change_type":     changeType,
			"unit":            unit,
			"source":          source,
			"quantity_before": nil,
			"quantity_after":  nil,
			"recipe_id":       nil,
		}
		if productID.Valid {
			row["product_id"] = productID.Int64
		}
		if qBefore.Valid {
			row["quantity_before"] = qBefore.Float64
		}
		if qAfter.Valid {
			row["quantity_after"] = qAfter.Float64
		}
		if recipeID.Valid {
			row["recipe_id"] = recipeID.Int64
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

func historyLimit(r *http.Request) int {
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}
	return limit
}

// RegisterHistory registers the read endpoints for inventory history.
func RegisterHistory(mux *http.ServeMux, db *sql.DB) {
	// GET /api/history - paginated activity log
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		limit := historyLimit(r)
		offset := 0
		if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
			if v, err := strconv.Atoi(offsetStr); err == nil && v >= 0 {
				offset = v
			}
		}

		var total int
		if err := db.QueryRow(`SELECT COUNT(*) FROM inventory_history`).Scan(&total); err != nil {
			WriteServerError(w, r, err)
			return
		}

		rows, err := db.Query(`SELECT `+historyCols+`
			FROM inventory_history
			ORDER BY changed_at DESC
			LIMIT ? OFFSET ?`, limit, offset)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer rows.Close()
		items, err := scanHistoryRows(rows)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"total": total, "rows": items})
	})

	// GET /api/history/item/{id} - history for a single stock lot
	mux.HandleFunc("GET /api/history/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		rows, err := db.Query(`SELECT `+historyCols+`
			FROM inventory_history
			WHERE inventory_id = ?
			ORDER BY changed_at DESC
			LIMIT ?`, id, historyLimit(r))
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer rows.Close()
		items, err := scanHistoryRows(rows)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, items)
	})

	// GET /api/history/product/{id} - history across all lots of a product.
	// History outlives products, so an unknown id yields an empty list.
	mux.HandleFunc("GET /api/history/product/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		rows, err := db.Query(`SELECT `+historyCols+`
			FROM inventory_history
			WHERE product_id = ?
			ORDER BY changed_at DESC, id DESC
			LIMIT ?`, id, historyLimit(r))
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer rows.Close()
		items, err := scanHistoryRows(rows)
		if err != nil {
			WriteServerError(w, r, err)
			return
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
			WriteServerError(w, r, err)
			return
		}
		defer rows.Close()

		var stats []map[string]any
		for rows.Next() {
			var inventoryID int64
			var itemName, lastChanged string
			var count int
			if err := rows.Scan(&inventoryID, &itemName, &count, &lastChanged); err != nil {
				WriteServerError(w, r, err)
				return
			}
			stats = append(stats, map[string]any{
				"inventory_id": inventoryID,
				"item_name":    itemName,
				"change_count": count,
				"last_changed": lastChanged,
			})
		}
		if err := rows.Err(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if stats == nil {
			stats = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, stats)
	})
}
