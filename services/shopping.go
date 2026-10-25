package services

import (
	"database/sql"
	"math"
	"sort"
	"strings"

	"kitchen_manager/units"
)

// ProductLevel is the aggregate stock of one product (all its lots).
type ProductLevel struct {
	ID     int64
	Name   string
	Unit   string  // product unit: preferred_unit, else the product's unit
	Total  float64 // sum of convertible lots, expressed in Unit
	Par    float64 // par level (0 = unset)
	LotIDs []int64 // ascending
}

// BelowPar reports whether a par rule exists and total stock is under it.
func (p ProductLevel) BelowPar() bool { return p.Par > 0 && p.Total < p.Par }

// Needed is the restock-to-par quantity (par - total), in Unit.
func (p ProductLevel) Needed() float64 { return math.Round((p.Par-p.Total)*1e4) / 1e4 }

// ProductLevels aggregates every product's lots, including products with no
// lots left. Lots whose unit cannot be converted to the product unit
// (different dimension or different count unit) are left out of the total.
func ProductLevels(q interface {
	Query(string, ...any) (*sql.Rows, error)
}) ([]ProductLevel, error) {
	rows, err := q.Query(`SELECT p.id, p.name, p.unit, p.preferred_unit, p.par_level, l.id, l.quantity, l.unit
		FROM products p LEFT JOIN stock_lots l ON l.product_id = p.id ORDER BY p.name COLLATE NOCASE, p.id, l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ProductLevel{}
	for rows.Next() {
		var id int64
		var name, unit, pref string
		var par float64
		var lotID sql.NullInt64
		var qty sql.NullFloat64
		var lotUnit sql.NullString
		if err := rows.Scan(&id, &name, &unit, &pref, &par, &lotID, &qty, &lotUnit); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != id {
			target := unit
			if pref != "" {
				target = pref
			}
			out = append(out, ProductLevel{ID: id, Name: name, Unit: target, Par: par})
		}
		pl := &out[len(out)-1]
		if !lotID.Valid {
			continue
		}
		pl.LotIDs = append(pl.LotIDs, lotID.Int64)
		c, err := units.Convert(qty.Float64, units.Unit(lotUnit.String), units.Unit(pl.Unit))
		if err != nil {
			continue // unconvertible lot: excluded from the total
		}
		pl.Total += c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// GenerateFromThresholds compares each product's total stock (all lots,
// converted to the product unit) against its par level and, when below par,
// adds one shopping-list row for par - total (restock to par), unless the
// product already has an unchecked row. Products with no stock left count.
// Returns the number added.
func GenerateFromThresholds(db *sql.DB) (int, error) {
	levels, err := ProductLevels(db)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, p := range levels {
		if !p.BelowPar() {
			continue
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM shopping_list WHERE checked=0 AND (product_id=? OR (source='threshold' AND name=? COLLATE NOCASE))`, p.ID, p.Name).Scan(&count); err != nil {
			return 0, err
		}
		if count > 0 {
			continue
		}
		if _, err := db.Exec(`INSERT INTO shopping_list (product_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,'threshold')`,
			p.ID, p.Name, p.Needed(), p.Unit); err != nil {
			return 0, err
		}
		added++
	}
	return added, nil
}
