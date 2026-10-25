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

var invalidUnitMsg = "invalid unit; valid units: " + units.ValidList()

// putAwayOverrides are the optional per-row adjustments for a put-away.
type putAwayOverrides struct {
	Quantity       *float64 `json:"quantity"`
	Unit           *string  `json:"unit"`
	Location       *string  `json:"location"`
	ExpirationDate *string  `json:"expiration_date"`
	ProductID      *int64   `json:"product_id"`
	InventoryID    *int64   `json:"inventory_id"` // legacy: a stock lot id
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
	err := tx.QueryRow(`SELECT product_id,name,quantity_needed,unit FROM shopping_list WHERE id=?`, shoppingID).
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

	// Resolve the product: explicit override, the row's link, else an existing
	// product with the same normalised name (matches how threshold generation
	// dedups by name), else a brand-new product.
	var prod product
	found := false
	loadProduct := func(id int64) (bool, error) {
		p, err := getProduct(tx, id)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		prod = p
		return true, nil
	}
	switch {
	case o.ProductID != nil:
		ok, err := loadProduct(*o.ProductID)
		if err != nil {
			return res, err
		}
		if !ok {
			return res, errItemNotFound
		}
		found = true
	case o.InventoryID != nil: // legacy: a stock lot id
		pid, err := productOfLot(tx, *o.InventoryID)
		if err != nil {
			return res, err
		}
		if found, err = loadProduct(pid); err != nil {
			return res, err
		}
	case linked.Valid:
		if found, err = loadProduct(linked.Int64); err != nil {
			return res, err
		}
	}
	norm := strings.TrimSpace(name)
	if !found {
		p, ok, err := findProductByName(tx, norm)
		if err != nil {
			return res, err
		}
		if ok {
			prod, found = p, true
		}
	}

	location := ""
	if found {
		err := tx.QueryRow(`SELECT location FROM stock_lots WHERE product_id=? ORDER BY quantity > 0 DESC, id LIMIT 1`, prod.ID).Scan(&location)
		if err != nil && err != sql.ErrNoRows {
			return res, err
		}
	}
	if o.Location != nil && *o.Location != "" { // blank means "use the product's location"
		location = *o.Location
	}

	// Convert into the product's effective unit before any write.
	eff := prod.EffUnit()
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
	histName := prod.Name
	if !found {
		p, _, err := getOrCreateProduct(tx, norm, reqUnit, "")
		if err != nil {
			return res, err
		}
		prod = p
		r, err := tx.Exec(`INSERT INTO stock_lots (product_id,quantity,unit,location,expiration_date) VALUES (?,?,?,?,?)`,
			prod.ID, qty, reqUnit, location, expiry)
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
		lotUnit := eff
		if lotUnit == "" {
			lotUnit = reqUnit
		}
		var lotID int64
		err := tx.QueryRow(`SELECT id FROM stock_lots WHERE product_id=? AND unit=? AND location=? AND expiration_date=? ORDER BY id LIMIT 1`,
			prod.ID, lotUnit, location, expiry).Scan(&lotID)
		switch {
		case err == nil:
			if before, after, err = addStock(tx, lotID, converted); err != nil {
				return res, err
			}
			invID = lotID
		case err == sql.ErrNoRows:
			r, err := tx.Exec(`INSERT INTO stock_lots (product_id,quantity,unit,location,expiration_date) VALUES (?,?,?,?,?)`,
				prod.ID, converted, lotUnit, location, expiry)
			if err != nil {
				return res, err
			}
			invID, _ = r.LastInsertId()
			after = converted
			res.Created = true
		default:
			return res, err
		}
		histUnit = lotUnit
		res.Added = converted
	}
	res.InventoryID = invID
	res.Unit = histUnit

	if err := LogHistory(db, tx, LogHistoryParams{
		InventoryID: invID, ProductID: prod.ID, ItemName: histName, ChangeType: "add",
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
			WriteServerError(w, r, err)
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
				WriteServerError(w, r, err)
			}
			return
		}
		lot, err := getLot(tx, res.InventoryID)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
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
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback()
		rows, err := tx.Query(`SELECT id,name FROM shopping_list WHERE checked=1 ORDER BY id`)
		if err != nil {
			WriteServerError(w, r, err)
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
				WriteServerError(w, r, err)
				return
			}
			checked = append(checked, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			WriteServerError(w, r, err)
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
				WriteServerError(w, r, err)
				return
			}
			lot, err := getLot(tx, res.InventoryID)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			putAway = append(putAway, map[string]any{"shopping_id": c.id, "inventory": lot, "created": res.Created})
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"put_away": putAway, "skipped": skipped})
		if len(putAway) > 0 {
			broadcast()
		}
	})
}
