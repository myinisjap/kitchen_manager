package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"math"

	"kitchen_manager/units"
)

var (
	errIncompatibleUnits = errors.New("incompatible units")
	errItemNotFound      = errors.New("inventory item not found")
)

type deductRequest struct {
	ItemID    int64   // anchor stock lot (resolved to its product)
	ProductID int64   // deduct by product instead of by anchor lot
	Qty       float64 // amount to remove, in Unit
	Unit      string  // requested unit; "" = already in the product's unit
	Source    string  // "manual", "meal_cooked", "barcode_remove", ...
	RecipeID  *int64  // for history (cooking)
	ChangedBy string
	SingleLot bool // deduct only from ItemID, no cascade
}

type deductResult struct {
	ProductID     int64
	ItemName      string
	Unit          string  // product unit the deduction was made in
	Requested     float64 // Qty converted into Unit
	Deducted      float64 // actually removed (capped by stock), in Unit
	ResultItemID  int64   // a lot that still has stock; 0 if none
	UnitCostCents int64   // total cost of what was consumed
}

// deductStock removes stock from a product, converting the requested unit into
// the product's unit (preferred_unit, falling back to unit) and consuming the
// earliest-expiring lots first across all of the product's lots (converting
// between lot units). Emptied lots are deleted; the product stays. Lots whose
// unit can't convert are skipped. The caller owns commit/rollback; all queries
// go through tx.
func deductStock(db *sql.DB, tx *sql.Tx, req deductRequest) (deductResult, error) {
	var res deductResult
	pid := req.ProductID
	if pid == 0 {
		var err error
		if pid, err = productOfLot(tx, req.ItemID); err != nil {
			return res, err
		}
	}
	prod, err := getProduct(tx, pid)
	if err == sql.ErrNoRows {
		return res, errItemNotFound
	}
	if err != nil {
		return res, err
	}
	effUnit := prod.EffUnit()
	res.ProductID = pid
	res.ItemName = prod.Name
	res.Unit = effUnit

	qty := req.Qty
	if req.Unit != "" && effUnit != "" && req.Unit != effUnit {
		qty, err = units.Convert(req.Qty, units.Unit(req.Unit), units.Unit(effUnit))
		if err != nil {
			return res, fmt.Errorf("%w: cannot deduct %g %s of %q from stock tracked in %s", errIncompatibleUnits, req.Qty, req.Unit, prod.Name, effUnit)
		}
	}
	res.Requested = qty

	type lot struct {
		id   int64
		qty  float64
		unit string
		cost int64
	}
	var lots []lot
	q := `SELECT id, quantity, unit, unit_cost_cents FROM stock_lots WHERE product_id=?`
	args := []any{pid}
	if req.SingleLot {
		q += ` AND id=?`
		args = append(args, req.ItemID)
	}
	q += ` ORDER BY CASE WHEN expiration_date='' THEN 1 ELSE 0 END ASC, expiration_date ASC, id ASC`
	rows, err := tx.Query(q, args...)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var l lot
		if err := rows.Scan(&l.id, &l.qty, &l.unit, &l.cost); err != nil {
			rows.Close()
			return res, err
		}
		lots = append(lots, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}

	remaining := qty // in effUnit
	var emptied []int64
	var cost float64
	for _, l := range lots {
		if remaining <= 0 {
			break
		}
		if l.qty <= 0 {
			continue
		}
		lotEff, cerr := units.Convert(l.qty, units.Unit(l.unit), units.Unit(effUnit))
		if cerr != nil {
			continue // lot can't be expressed in the product unit
		}
		before := l.qty
		var after, takenLot float64 // takenLot is in the lot's own unit
		if lotEff <= remaining {
			remaining -= lotEff
			res.Deducted += lotEff
			takenLot = l.qty
			emptied = append(emptied, l.id)
		} else {
			if l.unit == effUnit {
				takenLot = remaining
			} else {
				takenLot = l.qty * remaining / lotEff
			}
			after = l.qty - takenLot
			res.Deducted += remaining
			remaining = 0
			res.ResultItemID = l.id
			if _, err := tx.Exec(`UPDATE stock_lots SET quantity=? WHERE id=?`, after, l.id); err != nil {
				return res, err
			}
		}
		cost += takenLot * float64(l.cost)
		if err := LogHistory(db, tx, LogHistoryParams{
			InventoryID:    l.id,
			ProductID:      pid,
			ItemName:       prod.Name,
			ChangeType:     "deduct",
			QuantityBefore: &before,
			QuantityAfter:  &after,
			Unit:           l.unit,
			Source:         req.Source,
			RecipeID:       req.RecipeID,
			ChangedBy:      req.ChangedBy,
		}); err != nil {
			return res, err
		}
	}
	res.UnitCostCents = int64(math.Round(cost))

	if err := retireEmptyLots(tx, emptied); err != nil {
		return res, err
	}
	if res.ResultItemID == 0 {
		// Point at any lot that still has stock, if the cascade ended exactly on a lot boundary.
		if err := tx.QueryRow(`SELECT COALESCE((SELECT id FROM stock_lots WHERE product_id=? AND quantity>0
			ORDER BY CASE WHEN expiration_date='' THEN 1 ELSE 0 END, expiration_date, id LIMIT 1), 0)`, pid).Scan(&res.ResultItemID); err != nil {
			return res, err
		}
	}
	return res, nil
}
