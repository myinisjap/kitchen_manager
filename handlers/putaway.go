package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kitchen_manager/internal/auth"
	"kitchen_manager/units"
)

var (
	errShoppingNotFound = errors.New("shopping item not found")
	errInvalidPutAway   = errors.New("invalid put-away")
)

const invalidUnitMsg = "invalid unit; valid units: g, kg, oz, lb, ml, L, cup, tbsp, tsp, piece, clove, can, jar, bunch"

// putAwayOverrides are the optional per-row adjustments for a put-away.
type putAwayOverrides struct {
	Quantity       *float64 `json:"quantity"`
	Unit           *string  `json:"unit"`
	Location       *string  `json:"location"`
	ExpirationDate *string  `json:"expiration_date"`
	InventoryID    *int64   `json:"inventory_id"`
}

type putAwayResult struct {
	ShoppingID  int64
	InventoryID int64
	Created     bool
	Added       float64
	Unit        string
}

// putAwayShoppingItem moves one shopping row into inventory inside tx and
// deletes the row. All validation and unit conversion happen before the first
// write, so a returned validation error leaves nothing half-written.
// Everything goes through tx (the DB has a single connection).
func putAwayShoppingItem(db *sql.DB, tx *sql.Tx, shoppingID int64, o putAwayOverrides, changedBy string) (putAwayResult, error) {
	res := putAwayResult{ShoppingID: shoppingID}
	var linked sql.NullInt64
	var name, rowUnit string
	var qty float64
	err := tx.QueryRow(`SELECT inventory_id,name,quantity_needed,unit FROM shopping_list WHERE id=?`, shoppingID).
		Scan(&linked, &name, &qty, &rowUnit)
	if err == sql.ErrNoRows {
		return res, errShoppingNotFound
	}
	if err != nil {
		return res, err
	}

	if o.Quantity != nil {
		qty = *o.Quantity
	}
	if qty <= 0 {
		return res, fmt.Errorf("%w: quantity must be greater than 0", errInvalidPutAway)
	}
	reqUnit := rowUnit
	if o.Unit != nil {
		reqUnit = *o.Unit
	}
	if reqUnit != "" && !units.IsValid(units.Unit(reqUnit)) {
		return res, fmt.Errorf("%w: %s", errInvalidPutAway, invalidUnitMsg)
	}
	expiry := ""
	if o.ExpirationDate != nil && *o.ExpirationDate != "" {
		if _, err := time.Parse("2006-01-02", *o.ExpirationDate); err != nil {
			return res, fmt.Errorf("%w: expiration_date must be YYYY-MM-DD", errInvalidPutAway)
		}
		expiry = *o.ExpirationDate
	}

	// Resolve the product (anchor lot): explicit link, the row's link, else an
	// existing product with the same normalised name (matches how threshold
	// generation dedups by name), else a brand-new product.
	var anchorID int64
	var anchorName, anchorUnit, anchorPref, anchorLoc string
	found := false
	load := func(id int64) (bool, error) {
		err := tx.QueryRow(`SELECT id,name,unit,preferred_unit,location FROM inventory WHERE id=?`, id).
			Scan(&anchorID, &anchorName, &anchorUnit, &anchorPref, &anchorLoc)
		if err == sql.ErrNoRows {
			return false, nil
		}
		return err == nil, err
	}
	if o.InventoryID != nil {
		ok, err := load(*o.InventoryID)
		if err != nil {
			return res, err
		}
		if !ok {
			return res, errItemNotFound
		}
		found = true
	} else if linked.Valid {
		if found, err = load(linked.Int64); err != nil {
			return res, err
		}
	}
	norm := strings.ToLower(strings.TrimSpace(name))
	if !found {
		err := tx.QueryRow(`SELECT id,name,unit,preferred_unit,location FROM inventory WHERE name=? ORDER BY quantity > 0 DESC, id LIMIT 1`, norm).
			Scan(&anchorID, &anchorName, &anchorUnit, &anchorPref, &anchorLoc)
		if err != nil && err != sql.ErrNoRows {
			return res, err
		}
		found = err == nil
	}

	location := anchorLoc
	if !found {
		location = ""
	}
	if o.Location != nil && *o.Location != "" { // blank means "use the product's location"
		location = *o.Location
	}

	// Convert into the anchor's effective unit before any write.
	eff := anchorPref
	if eff == "" {
		eff = anchorUnit
	}
	converted := qty
	if found && reqUnit != "" && eff != "" && reqUnit != eff {
		bad := units.BaseDimension(units.Unit(reqUnit)) != units.BaseDimension(units.Unit(eff))
		if !bad {
			converted, err = units.Convert(qty, units.Unit(reqUnit), units.Unit(eff))
			bad = err != nil
		}
		if bad {
			return res, fmt.Errorf("%w: cannot add %g %s of %q to stock tracked in %s", errIncompatibleUnits, qty, reqUnit, name, eff)
		}
	}

	var invID int64
	var before, after float64
	histUnit := eff
	histName := anchorName
	if !found {
		r, err := tx.Exec(`INSERT INTO inventory (name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan) VALUES (?,?,?,?,?,0,'','',0,0)`,
			norm, qty, reqUnit, location, expiry)
		if err != nil {
			return res, err
		}
		invID, _ = r.LastInsertId()
		after = qty
		res.Created = true
		res.Added = qty
		histUnit = reqUnit
		histName = norm
	} else {
		// Pick the lot to receive stock: same name+unit as the anchor so
		// grouping and sibling logic keep working.
		var lotID int64
		err := tx.QueryRow(`SELECT id FROM inventory WHERE name=? AND unit=? AND location=? AND expiration_date=? ORDER BY id=? DESC, id LIMIT 1`,
			anchorName, anchorUnit, location, expiry, anchorID).Scan(&lotID)
		switch {
		case err == nil:
		case err == sql.ErrNoRows:
			err = tx.QueryRow(`SELECT id FROM inventory WHERE name=? AND unit=? AND quantity <= 0 ORDER BY id=? DESC, id LIMIT 1`,
				anchorName, anchorUnit, anchorID).Scan(&lotID)
			if err == nil {
				if _, err := tx.Exec(`UPDATE inventory SET location=?, expiration_date=? WHERE id=?`, location, expiry, lotID); err != nil {
					return res, err
				}
			} else if err == sql.ErrNoRows {
				lotID = 0
			} else {
				return res, err
			}
		default:
			return res, err
		}
		if lotID != 0 {
			if before, after, err = addStock(tx, lotID, converted); err != nil {
				return res, err
			}
			invID = lotID
		} else {
			var par float64
			if err := tx.QueryRow(`SELECT COALESCE(MAX(low_threshold),0) FROM inventory WHERE name=?`, anchorName).Scan(&par); err != nil {
				return res, err
			}
			r, err := tx.Exec(`INSERT INTO inventory (name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan) VALUES (?,?,?,?,?,?,'',?,0,0)`,
				anchorName, converted, anchorUnit, location, expiry, par, anchorPref)
			if err != nil {
				return res, err
			}
			invID, _ = r.LastInsertId()
			after = converted
			res.Created = true
		}
		res.Added = converted
	}
	res.InventoryID = invID
	res.Unit = histUnit

	if err := LogHistory(db, tx, LogHistoryParams{
		InventoryID: invID, ItemName: histName, ChangeType: "add",
		QuantityBefore: &before, QuantityAfter: &after, Unit: histUnit,
		Source: "shopping_bought", ChangedBy: changedBy,
	}); err != nil {
		return res, err
	}
	if _, err := tx.Exec(`DELETE FROM shopping_list_sources WHERE shopping_id=?`, shoppingID); err != nil {
		return res, err
	}
	del, err := tx.Exec(`DELETE FROM shopping_list WHERE id=?`, shoppingID)
	if err != nil {
		return res, err
	}
	if n, _ := del.RowsAffected(); n == 0 {
		return res, errShoppingNotFound
	}
	return res, nil
}

func readInventoryLot(tx *sql.Tx, id int64) (map[string]any, error) {
	return scanInventoryRow(tx.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit,unit_cost_cents,quantity_per_scan FROM inventory WHERE id=?`, id))
}

func registerPutAway(mux *http.ServeMux, db *sql.DB, broadcast func()) {
	mux.HandleFunc("POST /api/shopping/{id}/bought", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var o putAwayOverrides
		if err := ReadJSON(r, &o); err != nil && !errors.Is(err, io.EOF) {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback()
		res, err := putAwayShoppingItem(db, tx, id, o, auth.EmailFromContext(r.Context()))
		if err != nil {
			switch {
			case errors.Is(err, errShoppingNotFound), errors.Is(err, errItemNotFound):
				WriteError(w, http.StatusNotFound, err.Error())
			case errors.Is(err, errIncompatibleUnits), errors.Is(err, errInvalidPutAway):
				WriteError(w, http.StatusBadRequest, err.Error())
			default:
				WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		lot, err := readInventoryLot(tx, res.InventoryID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"shopping_id": res.ShoppingID, "inventory": lot, "created": res.Created,
			"added": res.Added, "unit": res.Unit,
		})
		broadcast()
	})

	// Puts away every checked row. Override entries for ids that aren't
	// checked are ignored. Rows that fail validation/unit checks are skipped
	// (left checked on the list) and reported; any other error rolls back all.
	mux.HandleFunc("POST /api/shopping/put-away", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Items []struct {
				ID int64 `json:"id"`
				putAwayOverrides
			} `json:"items"`
		}
		if err := ReadJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		overrides := map[int64]putAwayOverrides{}
		for _, it := range body.Items {
			overrides[it.ID] = it.putAwayOverrides
		}
		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback()
		rows, err := tx.Query(`SELECT id,name FROM shopping_list WHERE checked=1 ORDER BY id`)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		type checkedRow struct {
			id   int64
			name string
		}
		var checked []checkedRow
		for rows.Next() {
			var c checkedRow
			if err := rows.Scan(&c.id, &c.name); err != nil {
				rows.Close()
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			checked = append(checked, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		by := auth.EmailFromContext(r.Context())
		putAway := []map[string]any{}
		skipped := []map[string]any{}
		for _, c := range checked {
			res, err := putAwayShoppingItem(db, tx, c.id, overrides[c.id], by)
			if err != nil {
				if errors.Is(err, errIncompatibleUnits) || errors.Is(err, errInvalidPutAway) ||
					errors.Is(err, errItemNotFound) || errors.Is(err, errShoppingNotFound) {
					skipped = append(skipped, map[string]any{"shopping_id": c.id, "name": c.name, "reason": err.Error()})
					continue
				}
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			lot, err := readInventoryLot(tx, res.InventoryID)
			if err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			putAway = append(putAway, map[string]any{"shopping_id": c.id, "inventory": lot, "created": res.Created})
		}
		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"put_away": putAway, "skipped": skipped})
		if len(putAway) > 0 {
			broadcast()
		}
	})
}
