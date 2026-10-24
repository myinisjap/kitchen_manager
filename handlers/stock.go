package handlers

import (
	"database/sql"
	"strings"
)

// relinkInventoryRefs points every row that references inventory row `from`
// at row `to` instead, or clears the reference when to is nil. It must run
// before `from` is deleted so the delete doesn't trip a foreign key.
func relinkInventoryRefs(tx *sql.Tx, from int64, to *int64) error {
	for _, table := range []string{"recipe_ingredients", "shopping_list", "meal_history_ingredients"} {
		if _, err := tx.Exec(`UPDATE `+table+` SET inventory_id=? WHERE inventory_id=?`, to, from); err != nil {
			return err
		}
	}
	return nil
}

// moveBarcodes carries row `from`'s barcodes over to row `to` so scans keep
// resolving after `from` is deleted: SKU aliases move across, and the primary
// barcode becomes `to`'s primary barcode (if it has none) or a SKU alias.
func moveBarcodes(tx *sql.Tx, from, to int64) error {
	if _, err := tx.Exec(`UPDATE OR IGNORE inventory_skus SET inventory_id=? WHERE inventory_id=?`, to, from); err != nil {
		return err
	}
	var fromBarcode, toBarcode string
	var qtyPerScan float64
	if err := tx.QueryRow(`SELECT barcode, quantity_per_scan FROM inventory WHERE id=?`, from).Scan(&fromBarcode, &qtyPerScan); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT barcode FROM inventory WHERE id=?`, to).Scan(&toBarcode); err != nil {
		return err
	}
	if fromBarcode == "" || fromBarcode == toBarcode {
		return nil
	}
	if toBarcode == "" {
		_, err := tx.Exec(`UPDATE inventory SET barcode=? WHERE id=?`, fromBarcode, to)
		return err
	}
	if qtyPerScan <= 0 {
		qtyPerScan = 1
	}
	_, err := tx.Exec(`INSERT OR IGNORE INTO inventory_skus (inventory_id, barcode, quantity_per_scan) VALUES (?,?,?)`, to, fromBarcode, qtyPerScan)
	return err
}

// findSurvivingSibling returns another inventory row with the same name and
// unit that isn't in exclude, preferring rows that still have stock.
func findSurvivingSibling(tx *sql.Tx, name, unit string, exclude []int64) (int64, bool, error) {
	q := `SELECT id FROM inventory WHERE name=? AND unit=?`
	args := []any{name, unit}
	if len(exclude) > 0 {
		q += ` AND id NOT IN (?` + strings.Repeat(",?", len(exclude)-1) + `)`
		for _, id := range exclude {
			args = append(args, id)
		}
	}
	q += ` ORDER BY quantity > 0 DESC, id LIMIT 1`
	var id int64
	err := tx.QueryRow(q, args...).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// retireEmptyLots handles inventory rows whose stock has run out. When another
// row of the same item survives, the empty rows are folded into it and
// deleted. When none does, one row is kept at quantity 0 (with its expiry
// cleared) so the item stays visible as out of stock and threshold restocking
// and recipe links keep working. It returns the id of the row kept at zero,
// or 0 if every empty row was folded into a surviving sibling.
func retireEmptyLots(tx *sql.Tx, ids []int64, name, unit string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	survivor, ok, err := findSurvivingSibling(tx, name, unit, ids)
	if err != nil {
		return 0, err
	}
	var kept int64
	toDelete := ids
	if !ok {
		kept = ids[len(ids)-1]
		toDelete = ids[:len(ids)-1]
		survivor = kept
		if _, err := tx.Exec(`UPDATE inventory SET quantity=0, expiration_date='' WHERE id=?`, kept); err != nil {
			return 0, err
		}
	}
	for _, id := range toDelete {
		if err := relinkInventoryRefs(tx, id, &survivor); err != nil {
			return 0, err
		}
		if err := moveBarcodes(tx, id, survivor); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM inventory WHERE id=?`, id); err != nil {
			return 0, err
		}
	}
	return kept, nil
}
