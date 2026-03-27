package services

import (
	"database/sql"
)

// GenerateFromThresholds finds all inventory items below their low_threshold
// and adds them to the shopping list if not already present (unchecked).
// Returns the number of items added.
func GenerateFromThresholds(db *sql.DB) (int, error) {
	rows, err := db.Query(`SELECT id, name, quantity, low_threshold, unit FROM inventory WHERE quantity < low_threshold`)
	if err != nil {
		return 0, err
	}

	type belowThreshold struct {
		id        int64
		name      string
		unit      string
		qty       float64
		threshold float64
	}
	var candidates []belowThreshold
	for rows.Next() {
		var c belowThreshold
		if err := rows.Scan(&c.id, &c.name, &c.qty, &c.threshold, &c.unit); err != nil {
			continue
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	added := 0
	for _, c := range candidates {
		// Check if already on the shopping list (unchecked)
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM shopping_list WHERE inventory_id=? AND checked=0`, c.id).Scan(&count)
		if count > 0 {
			continue
		}
		needed := c.threshold - c.qty
		db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,'threshold')`,
			c.id, c.name, needed, c.unit)
		added++
	}
	return added, nil
}
