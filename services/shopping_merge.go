package services

import (
	"database/sql"
	"math"
	"strings"

	"kitchen_manager/units"
)

// MergeMode controls how a contribution is applied to an existing row.
type MergeMode int

const (
	// MergeAdd adds the quantity on top of the row (explicit user action).
	MergeAdd MergeMode = iota
	// MergeReplace replaces this source+ref's previous contribution (idempotent).
	MergeReplace
)

// ListAddition describes a need to put on the shopping list.
type ListAddition struct {
	InventoryID *int64
	Name        string
	Unit        string
	Quantity    float64
	Source      string // "calendar" | "recipe"
	Ref         string // week_start or recipe id
	Label       string
	Mode        MergeMode
}

// MergeResult reports what MergeIntoShoppingList did.
type MergeResult struct {
	ShoppingID int64
	Created    bool
	Quantity   float64
	Unit       string
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }

func normName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// MergeIntoShoppingList merges a need into the lowest-id unchecked row for the
// same item with a compatible unit, or inserts a new row. All work goes
// through tx (single DB connection).
func MergeIntoShoppingList(tx *sql.Tx, a ListAddition) (MergeResult, error) {
	keys := map[string]bool{normName(a.Name): true}
	if a.InventoryID != nil {
		var n string
		err := tx.QueryRow(`SELECT name FROM inventory WHERE id=?`, *a.InventoryID).Scan(&n)
		if err == nil {
			keys[normName(n)] = true
		} else if err != sql.ErrNoRows {
			return MergeResult{}, err
		}
	}

	type cand struct {
		id    int64
		inv   sql.NullInt64
		name  string
		unit  string
		qty   float64
		iname string
	}
	rows, err := tx.Query(`SELECT s.id, s.inventory_id, s.name, s.unit, s.quantity_needed, COALESCE(i.name,'')
		FROM shopping_list s LEFT JOIN inventory i ON i.id=s.inventory_id
		WHERE s.checked=0 ORDER BY s.id`)
	if err != nil {
		return MergeResult{}, err
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.inv, &c.name, &c.unit, &c.qty, &c.iname); err != nil {
			rows.Close()
			return MergeResult{}, err
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MergeResult{}, err
	}
	rows.Close()

	for _, c := range cands {
		match := keys[normName(c.name)] || (c.iname != "" && keys[normName(c.iname)]) ||
			(a.InventoryID != nil && c.inv.Valid && c.inv.Int64 == *a.InventoryID)
		if !match {
			continue
		}
		conv := a.Quantity
		if a.Unit != c.unit {
			v, err := units.Convert(a.Quantity, units.Unit(a.Unit), units.Unit(c.unit))
			if err != nil {
				continue
			}
			conv = v
		}
		conv = round4(conv)

		var prev float64
		err := tx.QueryRow(`SELECT quantity FROM shopping_list_sources WHERE shopping_id=? AND source=? AND ref=?`,
			c.id, a.Source, a.Ref).Scan(&prev)
		if err != nil && err != sql.ErrNoRows {
			return MergeResult{}, err
		}
		delta, newSrc := conv, prev+conv
		if a.Mode == MergeReplace {
			delta, newSrc = conv-prev, conv
		}
		newQty := round4(math.Max(c.qty+delta, 0))
		if delta != 0 {
			if _, err := tx.Exec(`UPDATE shopping_list SET quantity_needed=? WHERE id=?`, newQty, c.id); err != nil {
				return MergeResult{}, err
			}
		}
		if _, err := tx.Exec(`INSERT INTO shopping_list_sources (shopping_id,source,ref,label,quantity) VALUES (?,?,?,?,?)
			ON CONFLICT(shopping_id,source,ref) DO UPDATE SET quantity=excluded.quantity, label=excluded.label`,
			c.id, a.Source, a.Ref, a.Label, round4(newSrc)); err != nil {
			return MergeResult{}, err
		}
		if !c.inv.Valid && a.InventoryID != nil {
			if _, err := tx.Exec(`UPDATE shopping_list SET inventory_id=? WHERE id=?`, *a.InventoryID, c.id); err != nil {
				return MergeResult{}, err
			}
		}
		return MergeResult{ShoppingID: c.id, Quantity: newQty, Unit: c.unit}, nil
	}

	q := round4(a.Quantity)
	res, err := tx.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,?)`,
		a.InventoryID, a.Name, q, a.Unit, a.Source)
	if err != nil {
		return MergeResult{}, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO shopping_list_sources (shopping_id,source,ref,label,quantity) VALUES (?,?,?,?,?)`,
		id, a.Source, a.Ref, a.Label, q); err != nil {
		return MergeResult{}, err
	}
	return MergeResult{ShoppingID: id, Created: true, Quantity: q, Unit: a.Unit}, nil
}
