package handlers

import (
	"database/sql"
	"strings"

	"kitchen_manager/units"
)

// querier is satisfied by both *sql.DB and *sql.Tx. Inside a transaction always
// pass the tx: the pool has a single connection, so touching db while a tx is
// open deadlocks.
type querier interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// product is a kind of food. Stock lives in stock_lots rows under it; recipes,
// the shopping list and meal history reference products.
type product struct {
	ID            int64
	Name          string
	Unit          string // default unit (the first lot's unit)
	PreferredUnit string
	Par           float64 // 0 = unset
	Barcode       string  // primary barcode
	QtyPerScan    float64
}

// EffUnit is the unit the product's totals, par and deductions are expressed in.
func (p product) EffUnit() string {
	if p.PreferredUnit != "" {
		return p.PreferredUnit
	}
	return p.Unit
}

const productCols = `id,name,unit,preferred_unit,par_level,barcode,quantity_per_scan`

func scanProduct(row *sql.Row) (product, error) {
	var p product
	err := row.Scan(&p.ID, &p.Name, &p.Unit, &p.PreferredUnit, &p.Par, &p.Barcode, &p.QtyPerScan)
	return p, err
}

// getProduct loads a product by id (sql.ErrNoRows if missing).
func getProduct(q querier, id int64) (product, error) {
	return scanProduct(q.QueryRow(`SELECT `+productCols+` FROM products WHERE id=?`, id))
}

// findProductByName matches on the trimmed name, ignoring case.
func findProductByName(q querier, name string) (product, bool, error) {
	p, err := scanProduct(q.QueryRow(`SELECT `+productCols+` FROM products WHERE name=? COLLATE NOCASE`, strings.TrimSpace(name)))
	if err == sql.ErrNoRows {
		return product{}, false, nil
	}
	return p, err == nil, err
}

// findProductIDByName returns a pointer for storing in nullable product_id columns.
func findProductIDByName(q querier, name string) *int64 {
	p, ok, err := findProductByName(q, name)
	if err != nil || !ok {
		return nil
	}
	return &p.ID
}

// getOrCreateProduct returns the product with this name, creating it with the
// given units when it doesn't exist yet.
func getOrCreateProduct(q querier, name, unit, preferredUnit string) (product, bool, error) {
	name = strings.TrimSpace(name)
	p, ok, err := findProductByName(q, name)
	if err != nil || ok {
		return p, false, err
	}
	res, err := q.Exec(`INSERT INTO products (name,unit,preferred_unit) VALUES (?,?,?)`, name, unit, preferredUnit)
	if err != nil {
		return product{}, false, err
	}
	id, _ := res.LastInsertId()
	p, err = getProduct(q, id)
	return p, true, err
}

// productOfLot maps a stock lot id to its product id (errItemNotFound if the lot is gone).
func productOfLot(q querier, lotID int64) (int64, error) {
	var pid int64
	err := q.QueryRow(`SELECT product_id FROM stock_lots WHERE id=?`, lotID).Scan(&pid)
	if err == sql.ErrNoRows {
		return 0, errItemNotFound
	}
	return pid, err
}

// optProductID resolves a legacy lot id (as sent in `inventory_id`) to a
// product id pointer; nil when the lot doesn't exist.
func optProductID(q querier, lotID *int64) *int64 {
	if lotID == nil {
		return nil
	}
	pid, err := productOfLot(q, *lotID)
	if err != nil {
		return nil
	}
	return &pid
}

// productStock sums the product's lots, converted to its effective unit.
// Lots that can't convert (e.g. grams of something tracked in pieces) are left
// out of the total.
func productStock(q querier, productID int64) (total float64, unit string, err error) {
	p, err := getProduct(q, productID)
	if err != nil {
		return 0, "", err
	}
	unit = p.EffUnit()
	rows, err := q.Query(`SELECT quantity, unit FROM stock_lots WHERE product_id=?`, productID)
	if err != nil {
		return 0, unit, err
	}
	defer rows.Close()
	for rows.Next() {
		var qty float64
		var u string
		if err := rows.Scan(&qty, &u); err != nil {
			return 0, unit, err
		}
		c, cerr := units.Convert(qty, units.Unit(u), units.Unit(unit))
		if cerr != nil {
			continue
		}
		total += c
	}
	return total, unit, rows.Err()
}

// recommendedLotID is the lot to use next: earliest expiry among lots with
// stock, otherwise the newest lot. 0 if the product has no lots.
func recommendedLotID(q querier, productID int64) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM stock_lots WHERE product_id=?
		ORDER BY quantity>0 DESC, CASE WHEN expiration_date='' THEN 1 ELSE 0 END, expiration_date, id DESC LIMIT 1`, productID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// resolveBarcode finds the product owning a barcode: primary barcodes first,
// then SKU aliases (which carry their own quantity-per-scan).
func resolveBarcode(q querier, code string) (productID int64, qtyPerScan float64, skuID int64, matched bool, err error) {
	err = q.QueryRow(`SELECT id, quantity_per_scan FROM products WHERE barcode=?`, code).Scan(&productID, &qtyPerScan)
	if err == nil {
		return productID, qtyPerScan, 0, true, nil
	}
	if err != sql.ErrNoRows {
		return
	}
	err = q.QueryRow(`SELECT product_id, quantity_per_scan, id FROM product_skus WHERE barcode=?`, code).Scan(&productID, &qtyPerScan, &skuID)
	if err == sql.ErrNoRows {
		return 0, 0, 0, false, nil
	}
	return productID, qtyPerScan, skuID, err == nil, err
}

// barcodeTaken reports whether the barcode is a primary barcode or SKU alias
// of any product other than exceptProduct (0 = any).
func barcodeTaken(q querier, code string, exceptProduct int64) (bool, error) {
	var n int
	if err := q.QueryRow(`SELECT (SELECT COUNT(*) FROM products WHERE barcode=? AND id<>?) + (SELECT COUNT(*) FROM product_skus WHERE barcode=? AND product_id<>?)`,
		code, exceptProduct, code, exceptProduct).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// lotCols is the canonical lot+product SELECT; keep in sync with scanLot.
const lotCols = `l.id,p.name,l.quantity,l.unit,l.location,l.expiration_date,p.par_level,p.barcode,p.preferred_unit,l.unit_cost_cents,p.quantity_per_scan,l.product_id`
const lotFrom = ` FROM stock_lots l JOIN products p ON p.id=l.product_id`

// scanLot reads a row selected with lotCols into the API shape.
func scanLot(row rowScanner) (map[string]any, error) {
	var id, productID, unitCostCents int64
	var name, unit, location, expDate, barcode, preferredUnit string
	var qty, par, qtyPerScan float64
	if err := row.Scan(&id, &name, &qty, &unit, &location, &expDate, &par, &barcode, &preferredUnit, &unitCostCents, &qtyPerScan, &productID); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "product_id": productID, "name": name, "quantity": qty, "unit": unit,
		"preferred_unit": preferredUnit, "location": location,
		"expiration_date": expDate, "low_threshold": par, "barcode": barcode,
		"unit_cost_cents": unitCostCents, "quantity_per_scan": qtyPerScan,
	}, nil
}

func scanLots(rows *sql.Rows) ([]map[string]any, error) {
	items := []map[string]any{}
	for rows.Next() {
		m, err := scanLot(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

// getLot loads one lot in the API shape (sql.ErrNoRows if missing).
func getLot(q querier, id int64) (map[string]any, error) {
	return scanLot(q.QueryRow(`SELECT `+lotCols+lotFrom+` WHERE l.id=?`, id))
}

// productStub is the lot-shaped response for a product that has no lots left.
func productStub(q querier, productID int64) (map[string]any, error) {
	p, err := getProduct(q, productID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": 0, "product_id": p.ID, "name": p.Name, "quantity": 0.0, "unit": p.EffUnit(),
		"preferred_unit": p.PreferredUnit, "location": "", "expiration_date": "",
		"low_threshold": p.Par, "barcode": p.Barcode, "unit_cost_cents": 0, "quantity_per_scan": p.QtyPerScan,
	}, nil
}

// relinkProductRefs points recipe ingredients, shopping rows, meal history and
// stock history at product `to` (or clears the link when to is nil).
func relinkProductRefs(tx *sql.Tx, from int64, to *int64) error {
	for _, table := range []string{"recipe_ingredients", "shopping_list", "meal_history_ingredients"} {
		if _, err := tx.Exec(`UPDATE `+table+` SET product_id=? WHERE product_id=?`, to, from); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`UPDATE inventory_history SET product_id=? WHERE product_id=?`, to, from)
	return err
}

// moveProductBarcodes carries product `from`'s barcodes over to `to`: SKU
// aliases move across and the primary barcode becomes `to`'s primary (if it
// has none) or an alias.
func moveProductBarcodes(tx *sql.Tx, from, to int64) error {
	if _, err := tx.Exec(`UPDATE OR IGNORE product_skus SET product_id=? WHERE product_id=?`, to, from); err != nil {
		return err
	}
	src, err := getProduct(tx, from)
	if err != nil {
		return err
	}
	dst, err := getProduct(tx, to)
	if err != nil {
		return err
	}
	if src.Barcode == "" || src.Barcode == dst.Barcode {
		return nil
	}
	// Free the unique primary-barcode slot before it is reused.
	if _, err := tx.Exec(`UPDATE products SET barcode='' WHERE id=?`, from); err != nil {
		return err
	}
	if dst.Barcode == "" {
		_, err := tx.Exec(`UPDATE products SET barcode=?, quantity_per_scan=? WHERE id=?`, src.Barcode, src.QtyPerScan, to)
		return err
	}
	qps := src.QtyPerScan
	if qps <= 0 {
		qps = 1
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO product_skus (product_id,barcode,quantity_per_scan) VALUES (?,?,?)`, to, src.Barcode, qps)
	return err
}

// mergeProductInto moves every lot, link and barcode of product `from` onto
// `to` and deletes `from`. Par and preferred unit of `to` win; missing values
// are filled from `from`.
func mergeProductInto(tx *sql.Tx, from, to int64) error {
	if _, err := tx.Exec(`UPDATE stock_lots SET product_id=? WHERE product_id=?`, to, from); err != nil {
		return err
	}
	if err := moveProductBarcodes(tx, from, to); err != nil {
		return err
	}
	if err := relinkProductRefs(tx, from, &to); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE products SET
		par_level = MAX(par_level, (SELECT par_level FROM products WHERE id=?)),
		preferred_unit = CASE WHEN preferred_unit='' THEN (SELECT preferred_unit FROM products WHERE id=?) ELSE preferred_unit END
		WHERE id=?`, from, from, to); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM products WHERE id=?`, from)
	return err
}

// setProductPar sets the par level of a product.
func setProductPar(q querier, productID int64, par float64) error {
	_, err := q.Exec(`UPDATE products SET par_level=? WHERE id=?`, par, productID)
	return err
}

// lotCount is the number of lots under a product.
func lotCount(q querier, productID int64) (int, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM stock_lots WHERE product_id=?`, productID).Scan(&n)
	return n, err
}
