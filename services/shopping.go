package services

import (
	"database/sql"
	"math"
	"sort"

	"kitchen_manager/units"
)

// ProductLevel is the aggregate stock of one product (all lots sharing a name).
type ProductLevel struct {
	Name   string
	Unit   string  // product unit: first non-empty preferred_unit, else first lot's unit
	Total  float64 // sum of convertible lots, expressed in Unit
	Par    float64 // max low_threshold across lots (0 = unset)
	LotIDs []int64 // ascending
}

// BelowPar reports whether a par rule exists and total stock is under it.
func (p ProductLevel) BelowPar() bool { return p.Par > 0 && p.Total < p.Par }

// Needed is the restock-to-par quantity (par - total), in Unit.
func (p ProductLevel) Needed() float64 { return math.Round((p.Par-p.Total)*1e4) / 1e4 }

// ProductLevels aggregates all inventory lots by name.
// Lots whose unit cannot be converted to the product unit (different
// dimension or different count unit) are left out of the total.
func ProductLevels(q interface {
	Query(string, ...any) (*sql.Rows, error)
}) ([]ProductLevel, error) {
	rows, err := q.Query(`SELECT id, name, quantity, unit, preferred_unit, low_threshold FROM inventory ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type lot struct {
		id        int64
		qty       float64
		unit      string
		preferred string
	}
	byName := map[string][]lot{}
	par := map[string]float64{}
	for rows.Next() {
		var l lot
		var name string
		var thr float64
		if err := rows.Scan(&l.id, &name, &l.qty, &l.unit, &l.preferred, &thr); err != nil {
			return nil, err
		}
		byName[name] = append(byName[name], l)
		if thr > par[name] {
			par[name] = thr
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]ProductLevel, 0, len(byName))
	for name, lots := range byName {
		target := lots[0].unit
		for _, l := range lots {
			if l.preferred != "" {
				target = l.preferred
				break
			}
		}
		pl := ProductLevel{Name: name, Unit: target, Par: par[name]}
		for _, l := range lots {
			pl.LotIDs = append(pl.LotIDs, l.id)
			c, err := units.Convert(l.qty, units.Unit(l.unit), units.Unit(target))
			if err != nil {
				continue // unconvertible lot: excluded from the total
			}
			pl.Total += c
		}
		out = append(out, pl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GenerateFromThresholds compares each product's total stock (all lots of the
// same name, converted to the product unit) against its par level and, when
// below par, adds one shopping-list row for par - total (restock to par),
// unless the product already has an unchecked row. Returns the number added.
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
		if err := db.QueryRow(`SELECT COUNT(*) FROM shopping_list WHERE checked=0 AND (inventory_id IN (SELECT id FROM inventory WHERE name=?) OR (source='threshold' AND name=?))`, p.Name, p.Name).Scan(&count); err != nil {
			return 0, err
		}
		if count > 0 {
			continue
		}
		if _, err := db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,'threshold')`,
			p.LotIDs[0], p.Name, p.Needed(), p.Unit); err != nil {
			return 0, err
		}
		added++
	}
	return added, nil
}
