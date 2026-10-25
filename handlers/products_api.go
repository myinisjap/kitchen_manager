package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"kitchen_manager/internal/auth"
	"kitchen_manager/units"
)

// productJSON is the API shape of a product with its lots and SKU aliases.
func productJSON(q querier, id int64) (map[string]any, error) {
	p, err := getProduct(q, id)
	if err != nil {
		return nil, err
	}
	total, _, err := productStock(q, id)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(`SELECT `+lotCols+lotFrom+` WHERE l.product_id=?
		ORDER BY CASE WHEN l.expiration_date='' THEN 1 ELSE 0 END, l.expiration_date, l.id`, id)
	if err != nil {
		return nil, err
	}
	lots, err := scanLots(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	eff := p.EffUnit()
	for _, l := range lots {
		qty, _ := l["quantity"].(float64)
		u, _ := l["unit"].(string)
		_, cerr := units.Convert(qty, units.Unit(u), units.Unit(eff))
		l["unit_mismatch"] = cerr != nil
	}
	srows, err := q.Query(`SELECT id, barcode, quantity_per_scan FROM product_skus WHERE product_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	skus := []map[string]any{}
	for srows.Next() {
		var sid int64
		var code string
		var qps float64
		if err := srows.Scan(&sid, &code, &qps); err != nil {
			return nil, err
		}
		skus = append(skus, map[string]any{"id": sid, "barcode": code, "quantity_per_scan": qps})
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": p.ID, "name": p.Name, "unit": p.Unit, "preferred_unit": p.PreferredUnit,
		"effective_unit": eff, "par_level": p.Par, "barcode": p.Barcode,
		"quantity_per_scan": p.QtyPerScan, "total_quantity": total,
		"below_par": p.Par > 0 && total < p.Par, "out_of_stock": len(lots) == 0,
		"lots": lots, "skus": skus,
	}, nil
}

type productPatch struct {
	Name          *string  `json:"name"`
	PreferredUnit *string  `json:"preferred_unit"`
	ParLevel      *float64 `json:"par_level"`
	Barcode       *string  `json:"barcode"`
	QtyPerScan    *float64 `json:"quantity_per_scan"`
}

// RegisterProducts registers product-level endpoints (product ids, not lot ids).
func RegisterProducts(mux *http.ServeMux, db *sql.DB, hub ...*Hub) {
	var wsHub *Hub
	if len(hub) > 0 {
		wsHub = hub[0]
	}
	broadcast := func(events ...string) {
		if wsHub != nil {
			for _, e := range events {
				wsHub.Broadcast(e)
			}
		}
	}

	mux.HandleFunc("GET /api/products/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		out, err := productJSON(db, id)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "product not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("PATCH /api/products/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var p productPatch
		if err := ReadJSON(r, &p); err != nil {
			var ute *json.UnmarshalTypeError
			if errors.As(err, &ute) {
				WriteError(w, http.StatusBadRequest, "invalid type for field "+ute.Field)
				return
			}
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if p.Name == nil && p.PreferredUnit == nil && p.ParLevel == nil && p.Barcode == nil && p.QtyPerScan == nil {
			WriteError(w, http.StatusBadRequest, "no fields to update")
			return
		}
		if p.Name != nil {
			n := strings.TrimSpace(*p.Name)
			if n == "" {
				WriteError(w, http.StatusBadRequest, "name must not be empty")
				return
			}
			p.Name = &n
		}
		if p.ParLevel != nil && *p.ParLevel < 0 {
			WriteError(w, http.StatusBadRequest, "par_level must be >= 0")
			return
		}
		if p.QtyPerScan != nil && *p.QtyPerScan < 0 {
			WriteError(w, http.StatusBadRequest, "quantity_per_scan must be >= 0")
			return
		}

		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback()
		prod, err := getProduct(tx, id)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "product not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if p.PreferredUnit != nil && *p.PreferredUnit != "" {
			u := units.Unit(*p.PreferredUnit)
			if !units.IsValid(u) {
				WriteError(w, http.StatusBadRequest, "invalid preferred_unit")
				return
			}
			if prod.Unit != "" && units.BaseDimension(u) != units.BaseDimension(units.Unit(prod.Unit)) {
				WriteError(w, http.StatusBadRequest, "preferred_unit must be the same kind of unit as "+prod.Unit)
				return
			}
		}

		sets := []string{}
		args := []any{}
		if p.Name != nil {
			other, found, err := findProductByName(tx, *p.Name)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			if found && other.ID != id {
				WriteError(w, http.StatusConflict, "a product with that name already exists")
				return
			}
			sets = append(sets, "name=?")
			args = append(args, *p.Name)
		}
		if p.PreferredUnit != nil {
			sets = append(sets, "preferred_unit=?")
			args = append(args, *p.PreferredUnit)
		}
		if p.ParLevel != nil {
			sets = append(sets, "par_level=?")
			args = append(args, *p.ParLevel)
		}
		if p.QtyPerScan != nil {
			sets = append(sets, "quantity_per_scan=?")
			args = append(args, *p.QtyPerScan)
		}
		if p.Barcode != nil {
			if *p.Barcode != "" && *p.Barcode != prod.Barcode {
				taken, err := barcodeTaken(tx, *p.Barcode, id)
				if err != nil {
					WriteServerError(w, r, err)
					return
				}
				if taken {
					WriteError(w, http.StatusConflict, "barcode already assigned to another item")
					return
				}
				if _, err := tx.Exec(`DELETE FROM product_skus WHERE barcode=? AND product_id=?`, *p.Barcode, id); err != nil {
					WriteServerError(w, r, err)
					return
				}
			}
			sets = append(sets, "barcode=?")
			args = append(args, *p.Barcode)
		}
		args = append(args, id)
		if _, err := tx.Exec(`UPDATE products SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		out, err := productJSON(db, id)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, out)
		broadcast("inventory_updated")
	})

	mux.HandleFunc("POST /api/products/{id}/merge-into/{target_id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		target, ok2 := pathIDFromPattern(r, "target_id")
		if !ok || !ok2 {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		if id == target {
			WriteError(w, http.StatusBadRequest, "cannot merge a product into itself")
			return
		}
		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback()
		if _, err := getProduct(tx, id); err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "source product not found")
			return
		} else if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if _, err := getProduct(tx, target); err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "target product not found")
			return
		} else if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := mergeProductInto(tx, id, target); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		out, err := productJSON(db, target)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, out)
		broadcast("inventory_updated", "shopping_updated")
	})

	mux.HandleFunc("DELETE /api/products/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback()
		prod, err := getProduct(tx, id)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "product not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		type lotRow struct {
			id   int64
			qty  float64
			unit string
		}
		rows, err := tx.Query(`SELECT id, quantity, unit FROM stock_lots WHERE product_id=?`, id)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		var lots []lotRow
		for rows.Next() {
			var l lotRow
			if err := rows.Scan(&l.id, &l.qty, &l.unit); err != nil {
				rows.Close()
				WriteServerError(w, r, err)
				return
			}
			lots = append(lots, l)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		changedBy := auth.EmailFromContext(r.Context())
		for _, l := range lots {
			qty := l.qty
			if err := LogHistory(db, tx, LogHistoryParams{
				InventoryID: l.id, ProductID: id, ItemName: prod.Name, ChangeType: "delete",
				QuantityBefore: &qty, Unit: l.unit, Source: "manual", ChangedBy: changedBy,
			}); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		if _, err := tx.Exec(`DELETE FROM products WHERE id=?`, id); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		broadcast("inventory_updated", "shopping_updated")
	})
}
