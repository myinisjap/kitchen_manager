package handlers

import (
	"database/sql"
)

// retireEmptyLots deletes stock lots whose stock has run out. The product
// stays (so it still shows as out of stock, keeps its par level and its
// recipe/shopping links); only the lot goes.
func retireEmptyLots(tx *sql.Tx, ids []int64) error {
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM stock_lots WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// addStock atomically adds delta to stock lot id inside tx and returns the
// quantity before and after. Returns errItemNotFound if the lot is gone.
// Never write a quantity computed in Go: always use quantity = quantity + ?.
func addStock(tx *sql.Tx, id int64, delta float64) (before, after float64, err error) {
	if err = tx.QueryRow(`SELECT quantity FROM stock_lots WHERE id=?`, id).Scan(&before); err != nil {
		if err == sql.ErrNoRows {
			err = errItemNotFound
		}
		return
	}
	err = tx.QueryRow(`UPDATE stock_lots SET quantity = quantity + ? WHERE id=? RETURNING quantity`, delta, id).Scan(&after)
	return
}
