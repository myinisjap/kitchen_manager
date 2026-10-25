package handlers

import "database/sql"

// Lot is the API shape of a stock lot (product fields included for
// compatibility). Fields are declared in alphabetical json order so the output
// is byte-identical to the old map[string]any encoding (which sorted keys).
type Lot struct {
	Barcode         string   `json:"barcode"`
	Deducted        *float64 `json:"deducted,omitempty"` // POST /deduct only
	ExpirationDate  string   `json:"expiration_date"`
	ID              int64    `json:"id"`
	Location        string   `json:"location"`
	LotID           *int64   `json:"lot_id,omitempty"`          // stub after a lot ran out
	LowThreshold    float64  `json:"low_threshold"`             // product par
	MatchedBarcode  string   `json:"matched_barcode,omitempty"` // barcode lookup via SKU alias
	Name            string   `json:"name"`
	PreferredUnit   string   `json:"preferred_unit"`
	ProductID       int64    `json:"product_id"`
	Quantity        float64  `json:"quantity"`
	QuantityPerScan float64  `json:"quantity_per_scan"`
	SKUID           int64    `json:"sku_id,omitempty"`
	Unit            string   `json:"unit"`
	UnitCostCents   int64    `json:"unit_cost_cents"`
	UnitMismatch    *bool    `json:"unit_mismatch,omitempty"` // product view only; emitted even when false
}

// lotFallback is the minimal lot-like response used when the full lot can't be
// loaded. Alphabetical json order, like Lot.
type lotFallback struct {
	Deducted  *float64 `json:"deducted,omitempty"`
	ID        int64    `json:"id"`
	LotID     *int64   `json:"lot_id,omitempty"`
	ProductID int64    `json:"product_id"`
	Quantity  float64  `json:"quantity"`
}

// lotCols is the canonical lot+product SELECT; keep in sync with scanLot.
const lotCols = `l.id,p.name,l.quantity,l.unit,l.location,l.expiration_date,p.par_level,p.barcode,p.preferred_unit,l.unit_cost_cents,p.quantity_per_scan,l.product_id`
const lotFrom = ` FROM stock_lots l JOIN products p ON p.id=l.product_id`

type rowScanner interface{ Scan(...any) error }

// scanLot reads a row selected with lotCols.
func scanLot(row rowScanner) (Lot, error) {
	var l Lot
	err := row.Scan(&l.ID, &l.Name, &l.Quantity, &l.Unit, &l.Location, &l.ExpirationDate, &l.LowThreshold,
		&l.Barcode, &l.PreferredUnit, &l.UnitCostCents, &l.QuantityPerScan, &l.ProductID)
	return l, err
}

// scanLots reads all rows; the result is never nil so it encodes as [].
func scanLots(rows *sql.Rows) ([]Lot, error) {
	items := []Lot{}
	for rows.Next() {
		l, err := scanLot(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, rows.Err()
}

func queryLots(q querier, where string, args ...any) ([]Lot, error) {
	rows, err := q.Query(`SELECT `+lotCols+lotFrom+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLots(rows)
}

// getLot loads one lot (sql.ErrNoRows if missing).
func getLot(q querier, id int64) (Lot, error) {
	return scanLot(q.QueryRow(`SELECT `+lotCols+lotFrom+` WHERE l.id=?`, id))
}

type lotFilter struct{ Name, Location string }

// listLots lists lots ordered by id, optionally filtered by product name
// (substring) and location.
func listLots(q querier, f lotFilter) ([]Lot, error) {
	where := ` WHERE 1=1`
	args := []any{}
	if f.Name != "" {
		where += ` AND p.name LIKE ?`
		args = append(args, "%"+f.Name+"%")
	}
	if f.Location != "" {
		where += ` AND l.location=?`
		args = append(args, f.Location)
	}
	return queryLots(q, where+` ORDER BY l.id`, args...)
}

// expiringLots lists lots with an expiry date on or before cutoff.
func expiringLots(q querier, cutoff string) ([]Lot, error) {
	return queryLots(q, ` WHERE l.expiration_date != '' AND l.expiration_date <= ?`, cutoff)
}

// lotsOfProduct lists a product's lots, earliest expiry first.
func lotsOfProduct(q querier, productID int64) ([]Lot, error) {
	return queryLots(q, ` WHERE l.product_id=?
		ORDER BY CASE WHEN l.expiration_date='' THEN 1 ELSE 0 END, l.expiration_date, l.id`, productID)
}

// productStub is the lot-shaped response for a product that has no lots left.
func productStub(q querier, productID int64) (Lot, error) {
	p, err := getProduct(q, productID)
	if err != nil {
		return Lot{}, err
	}
	return Lot{
		ProductID: p.ID, Name: p.Name, Unit: p.EffUnit(), PreferredUnit: p.PreferredUnit,
		LowThreshold: p.Par, Barcode: p.Barcode, QuantityPerScan: p.QtyPerScan,
	}, nil
}

// lotCore is the handful of lot columns the mutating handlers need.
type lotCore struct {
	ID, ProductID int64
	Quantity      float64
	Unit          string
}

// getLotCore loads a lot's quantity, unit and product (sql.ErrNoRows if missing).
func getLotCore(q querier, id int64) (lotCore, error) {
	c := lotCore{ID: id}
	err := q.QueryRow(`SELECT quantity, unit, product_id FROM stock_lots WHERE id=?`, id).Scan(&c.Quantity, &c.Unit, &c.ProductID)
	return c, err
}

// deleteLot removes a lot.
func deleteLot(q querier, id int64) error {
	_, err := q.Exec(`DELETE FROM stock_lots WHERE id=?`, id)
	return err
}
