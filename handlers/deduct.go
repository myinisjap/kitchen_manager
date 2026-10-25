package handlers

import (
	"database/sql"
	"errors"
	"fmt"

	"kitchen_manager/units"
)

var (
	errIncompatibleUnits = errors.New("incompatible units")
	errItemNotFound      = errors.New("inventory item not found")
)

type deductRequest struct {
	ItemID    int64   // anchor inventory row
	Qty       float64 // amount to remove, in Unit
	Unit      string  // requested unit; "" = already in the item's unit
	Source    string  // "manual", "meal_cooked", "barcode_remove", ...
	RecipeID  *int64  // for history (cooking)
	ChangedBy string
	SingleLot bool // deduct only from ItemID, no cascade
}

type deductResult struct {
	ItemName      string
	Unit          string  // inventory unit the deduction was made in
	Requested     float64 // Qty converted into Unit
	Deducted      float64 // actually removed (capped by stock)
	LastLotID     int64   // lot that still has stock (0 if none)
	ResultItemID  int64   // row references should now point at; 0 if none
	UnitCostCents int64
}

// deductStock removes stock from an item, converting the requested unit into
// the item's unit (preferred_unit, falling back to unit), consuming the
// earliest-expiring lots first, and retiring emptied lots. The caller owns
// commit/rollback; all queries go through tx.
func deductStock(db *sql.DB, tx *sql.Tx, req deductRequest) (deductResult, error) {
	var res deductResult
	var name, unit, pref string
	var cost sql.NullInt64
	err := tx.QueryRow(`SELECT name, unit, preferred_unit, unit_cost_cents FROM inventory WHERE id=?`, req.ItemID).
		Scan(&name, &unit, &pref, &cost)
	if err == sql.ErrNoRows {
		return res, errItemNotFound
	}
	if err != nil {
		return res, err
	}
	effUnit := pref
	if effUnit == "" {
		effUnit = unit
	}
	res.ItemName = name
	res.Unit = effUnit
	res.UnitCostCents = cost.Int64

	qty := req.Qty
	if req.Unit != "" && effUnit != "" && req.Unit != effUnit {
		qty, err = units.Convert(req.Qty, units.Unit(req.Unit), units.Unit(effUnit))
		if err != nil {
			return res, fmt.Errorf("%w: cannot deduct %g %s of %q from stock tracked in %s", errIncompatibleUnits, req.Qty, req.Unit, name, effUnit)
		}
	}
	res.Requested = qty

	type lot struct {
		id  int64
		qty float64
	}
	var lots []lot
	if req.SingleLot {
		var l lot
		l.id = req.ItemID
		if err := tx.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, req.ItemID).Scan(&l.qty); err != nil {
			return res, err
		}
		lots = []lot{l}
	} else {
		rows, err := tx.Query(`SELECT id, quantity FROM inventory WHERE name=? COLLATE NOCASE AND unit=?
			ORDER BY CASE WHEN expiration_date='' THEN 1 ELSE 0 END ASC, expiration_date ASC, id ASC`, name, unit)
		if err != nil {
			return res, err
		}
		for rows.Next() {
			var l lot
			if err := rows.Scan(&l.id, &l.qty); err != nil {
				rows.Close()
				return res, err
			}
			lots = append(lots, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return res, err
		}
	}

	remainingDeduct := qty
	var emptied []int64
	for _, l := range lots {
		if remainingDeduct <= 0 {
			break
		}
		if l.qty <= 0 {
			continue
		}
		before := l.qty
		var after float64
		if l.qty <= remainingDeduct {
			remainingDeduct -= l.qty
			res.Deducted += l.qty
			emptied = append(emptied, l.id)
		} else {
			after = l.qty - remainingDeduct
			res.Deducted += remainingDeduct
			remainingDeduct = 0
			res.LastLotID = l.id
			if _, err := tx.Exec(`UPDATE inventory SET quantity=? WHERE id=?`, after, l.id); err != nil {
				return res, err
			}
		}
		if err := LogHistory(db, tx, LogHistoryParams{
			InventoryID:    l.id,
			ItemName:       name,
			ChangeType:     "deduct",
			QuantityBefore: &before,
			QuantityAfter:  &after,
			Unit:           effUnit,
			Source:         req.Source,
			RecipeID:       req.RecipeID,
			ChangedBy:      req.ChangedBy,
		}); err != nil {
			return res, err
		}
	}

	kept, survivor, err := retireEmptyLots(tx, emptied, name, unit)
	if err != nil {
		return res, err
	}
	switch {
	case res.LastLotID != 0:
		res.ResultItemID = res.LastLotID
	case kept != 0:
		res.ResultItemID = kept
		res.LastLotID = kept
	case survivor != 0:
		res.ResultItemID = survivor
	default:
		// nothing was emptied; anchor row still exists
		res.ResultItemID = req.ItemID
	}
	return res, nil
}
