// Package testutil provides shared test helpers.
package testutil

import (
	"database/sql"
	"testing"

	"kitchen_manager/internal/database"
)

// NewDB opens an in-memory SQLite database using the production schema
// (database.OpenDB) and closes it when the test ends.
func NewDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := database.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// LotOpt tweaks the product or lot created by AddLot.
type LotOpt func(*lotSpec)

type lotSpec struct {
	location, expiry, preferred, barcode string
	par, qps                             float64
	cost                                 int64
}

func WithLocation(l string) LotOpt      { return func(s *lotSpec) { s.location = l } }
func WithExpiry(e string) LotOpt        { return func(s *lotSpec) { s.expiry = e } }
func WithPreferredUnit(u string) LotOpt { return func(s *lotSpec) { s.preferred = u } }
func WithBarcode(b string) LotOpt       { return func(s *lotSpec) { s.barcode = b } }
func WithPar(p float64) LotOpt          { return func(s *lotSpec) { s.par = p } }
func WithQtyPerScan(q float64) LotOpt   { return func(s *lotSpec) { s.qps = q } }
func WithCost(c int64) LotOpt           { return func(s *lotSpec) { s.cost = c } }

// AddLot adds a stock lot, creating the product (matched by name, ignoring
// case) if needed. Product-level options (preferred unit, par, barcode, qty
// per scan) are applied when set. Returns the product and lot ids.
func AddLot(t testing.TB, db *sql.DB, name string, qty float64, unit string, opts ...LotOpt) (productID, lotID int64) {
	t.Helper()
	var s lotSpec
	for _, o := range opts {
		o(&s)
	}
	err := db.QueryRow(`SELECT id FROM products WHERE name=? COLLATE NOCASE`, name).Scan(&productID)
	if err == sql.ErrNoRows {
		res, err := db.Exec(`INSERT INTO products (name,unit) VALUES (?,?)`, name, unit)
		if err != nil {
			t.Fatal(err)
		}
		productID, _ = res.LastInsertId()
	} else if err != nil {
		t.Fatal(err)
	}
	if s.preferred != "" {
		mustExec(t, db, `UPDATE products SET preferred_unit=? WHERE id=?`, s.preferred, productID)
	}
	if s.par > 0 {
		mustExec(t, db, `UPDATE products SET par_level=? WHERE id=?`, s.par, productID)
	}
	if s.barcode != "" {
		mustExec(t, db, `UPDATE products SET barcode=? WHERE id=?`, s.barcode, productID)
	}
	if s.qps > 0 {
		mustExec(t, db, `UPDATE products SET quantity_per_scan=? WHERE id=?`, s.qps, productID)
	}
	res, err := db.Exec(`INSERT INTO stock_lots (product_id,quantity,unit,location,expiration_date,unit_cost_cents) VALUES (?,?,?,?,?,?)`,
		productID, qty, unit, s.location, s.expiry, s.cost)
	if err != nil {
		t.Fatal(err)
	}
	lotID, _ = res.LastInsertId()
	return productID, lotID
}

func mustExec(t testing.TB, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
