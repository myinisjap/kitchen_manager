package database

import (
	"database/sql"
	"log"
	"strings"
)

// asciiLower matches SQLite's NOCASE collation (ASCII-only folding), so the
// grouping done here agrees with the products_name_idx unique index.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// migrateProductsAndStockLots splits the one-row-per-lot inventory table into
// products (name, units, par level, barcodes) and stock_lots (quantity,
// location, expiry). Lot ids are preserved. Recipe ingredients, shopping list
// rows and meal history ingredients are re-pointed from a lot to its product.
// Runs with foreign_keys=OFF (see runMigration).
func migrateProductsAndStockLots(tx *sql.Tx) error {
	for _, s := range []string{
		`CREATE TABLE products (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			name              TEXT NOT NULL,
			unit              TEXT NOT NULL DEFAULT '',
			preferred_unit    TEXT NOT NULL DEFAULT '',
			par_level         REAL NOT NULL DEFAULT 0,
			barcode           TEXT NOT NULL DEFAULT '',
			quantity_per_scan REAL NOT NULL DEFAULT 1
		)`,
		`CREATE UNIQUE INDEX products_name_idx ON products(name COLLATE NOCASE)`,
		`CREATE UNIQUE INDEX products_barcode_idx ON products(barcode) WHERE barcode <> ''`,
		`CREATE TABLE stock_lots (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			product_id      INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			quantity        REAL NOT NULL DEFAULT 0,
			unit            TEXT NOT NULL DEFAULT '',
			location        TEXT NOT NULL DEFAULT '',
			expiration_date TEXT NOT NULL DEFAULT '',
			unit_cost_cents INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX stock_lots_product_idx ON stock_lots(product_id, expiration_date)`,
		`CREATE TABLE product_skus (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			product_id        INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			barcode           TEXT NOT NULL UNIQUE,
			quantity_per_scan REAL NOT NULL DEFAULT 1
		)`,
	} {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}

	type lot struct {
		id                   int64
		name, unit, loc, exp string
		barcode, pref        string
		qty, thr, qps        float64
		cost                 int64
	}
	rows, err := tx.Query(`SELECT id, name, quantity, unit, location, expiration_date, low_threshold, barcode, preferred_unit, unit_cost_cents, quantity_per_scan FROM inventory ORDER BY id`)
	if err != nil {
		return err
	}
	var lots []lot
	for rows.Next() {
		var l lot
		if err := rows.Scan(&l.id, &l.name, &l.qty, &l.unit, &l.loc, &l.exp, &l.thr, &l.barcode, &l.pref, &l.cost, &l.qps); err != nil {
			rows.Close()
			return err
		}
		lots = append(lots, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	type prod struct {
		id         int64
		name, unit string
		pref       string
		par        float64
		barcode    string
		qps        float64
		unitsSeen  map[string]bool
	}
	byKey := map[string]*prod{}
	var order []*prod
	lotProduct := map[int64]*prod{}
	for _, l := range lots {
		key := asciiLower(strings.TrimSpace(l.name))
		p := byKey[key]
		if p == nil {
			p = &prod{id: l.id, name: strings.TrimSpace(l.name), unit: l.unit, qps: 1, unitsSeen: map[string]bool{}}
			byKey[key] = p
			order = append(order, p)
		}
		if p.pref == "" {
			p.pref = l.pref
		}
		if l.thr > p.par {
			p.par = l.thr
		}
		p.unitsSeen[l.unit] = true
		lotProduct[l.id] = p
	}

	claimed := map[string]bool{}
	// Primary barcode: lowest-id lot with one, unless another product has it.
	for _, l := range lots {
		p := lotProduct[l.id]
		if p.barcode != "" || l.barcode == "" || claimed[l.barcode] {
			continue
		}
		p.barcode = l.barcode
		p.qps = l.qps
		if p.qps <= 0 {
			p.qps = 1
		}
		claimed[l.barcode] = true
	}
	mixed := 0
	for _, p := range order {
		if _, err := tx.Exec(`INSERT INTO products (id,name,unit,preferred_unit,par_level,barcode,quantity_per_scan) VALUES (?,?,?,?,?,?,?)`,
			p.id, p.name, p.unit, p.pref, p.par, p.barcode, p.qps); err != nil {
			return err
		}
		if len(p.unitsSeen) > 1 {
			mixed++
		}
	}
	if mixed > 0 {
		log.Printf("db: %d products have lots in more than one unit; lots that cannot convert to the product unit are kept but excluded from totals", mixed)
	}
	for _, l := range lots {
		p := lotProduct[l.id]
		if _, err := tx.Exec(`INSERT INTO stock_lots (id,product_id,quantity,unit,location,expiration_date,unit_cost_cents) VALUES (?,?,?,?,?,?,?)`,
			l.id, p.id, l.qty, l.unit, l.loc, l.exp, l.cost); err != nil {
			return err
		}
	}
	// Remaining distinct lot barcodes become SKU aliases of the lot's product.
	for _, l := range lots {
		if l.barcode == "" || claimed[l.barcode] && lotProduct[l.id].barcode == l.barcode {
			continue
		}
		var owner int64
		err := tx.QueryRow(`SELECT id FROM products WHERE barcode=?`, l.barcode).Scan(&owner)
		if err == nil {
			if owner != lotProduct[l.id].id {
				log.Printf("db: barcode %q is used by several products; keeping it on product %d only", l.barcode, owner)
			}
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		qps := l.qps
		if qps <= 0 {
			qps = 1
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO product_skus (product_id,barcode,quantity_per_scan) VALUES (?,?,?)`, lotProduct[l.id].id, l.barcode, qps); err != nil {
			return err
		}
	}
	// Existing SKU aliases.
	skuRows, err := tx.Query(`SELECT inventory_id, barcode, quantity_per_scan FROM inventory_skus ORDER BY id`)
	if err != nil {
		return err
	}
	type sku struct {
		inv int64
		bc  string
		qps float64
	}
	var skus []sku
	for skuRows.Next() {
		var s sku
		if err := skuRows.Scan(&s.inv, &s.bc, &s.qps); err != nil {
			skuRows.Close()
			return err
		}
		skus = append(skus, s)
	}
	skuRows.Close()
	for _, s := range skus {
		p := lotProduct[s.inv]
		if p == nil {
			continue
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM products WHERE barcode=?`, s.bc).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if s.qps <= 0 {
			s.qps = 1
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO product_skus (product_id,barcode,quantity_per_scan) VALUES (?,?,?)`, p.id, s.bc, s.qps); err != nil {
			return err
		}
	}

	// Re-point the referencing tables from lot ids to product ids.
	const prodOf = `(SELECT product_id FROM stock_lots WHERE id = inventory_id)`
	for _, s := range []string{
		`CREATE TABLE shopping_list_new (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			product_id       INTEGER REFERENCES products(id) ON DELETE SET NULL,
			name             TEXT    NOT NULL,
			quantity_needed  REAL    NOT NULL DEFAULT 1,
			unit             TEXT    NOT NULL DEFAULT '',
			checked          INTEGER NOT NULL DEFAULT 0,
			source           TEXT    NOT NULL DEFAULT 'manual'
		)`,
		`INSERT INTO shopping_list_new (id, product_id, name, quantity_needed, unit, checked, source)
			SELECT id, ` + prodOf + `, name, quantity_needed, unit, checked, source FROM shopping_list`,
		`DROP TABLE shopping_list`,
		`ALTER TABLE shopping_list_new RENAME TO shopping_list`,

		`CREATE TABLE recipe_ingredients_new (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			recipe_id    INTEGER NOT NULL REFERENCES recipes(id),
			product_id   INTEGER REFERENCES products(id) ON DELETE SET NULL,
			name         TEXT    NOT NULL,
			quantity     REAL    NOT NULL,
			unit         TEXT    NOT NULL DEFAULT ''
		)`,
		`INSERT INTO recipe_ingredients_new (id, recipe_id, product_id, name, quantity, unit)
			SELECT id, recipe_id, ` + prodOf + `, name, quantity, unit FROM recipe_ingredients`,
		`DROP TABLE recipe_ingredients`,
		`ALTER TABLE recipe_ingredients_new RENAME TO recipe_ingredients`,

		`CREATE TABLE meal_history_ingredients_new (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			meal_history_id INTEGER NOT NULL REFERENCES meal_history(id),
			product_id      INTEGER REFERENCES products(id) ON DELETE SET NULL,
			ingredient_name TEXT    NOT NULL,
			quantity_used   REAL    NOT NULL,
			unit            TEXT    NOT NULL DEFAULT '',
			cost_cents      INTEGER
		)`,
		`INSERT INTO meal_history_ingredients_new (id, meal_history_id, product_id, ingredient_name, quantity_used, unit, cost_cents)
			SELECT id, meal_history_id, ` + prodOf + `, ingredient_name, quantity_used, unit, cost_cents FROM meal_history_ingredients`,
		`DROP TABLE meal_history_ingredients`,
		`ALTER TABLE meal_history_ingredients_new RENAME TO meal_history_ingredients`,

		// History keeps the lot id and gains the product id (no FK: history
		// must outlive products).
		`ALTER TABLE inventory_history ADD COLUMN product_id INTEGER`,
		`UPDATE inventory_history SET product_id = (SELECT product_id FROM stock_lots WHERE id = inventory_history.inventory_id)`,
		`UPDATE inventory_history SET product_id = (SELECT id FROM products WHERE name = TRIM(inventory_history.item_name) COLLATE NOCASE)
			WHERE product_id IS NULL`,
		`CREATE INDEX inventory_history_product_idx ON inventory_history(product_id, changed_at)`,

		`DROP TABLE inventory_skus`,
		`DROP TABLE inventory`,
	} {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	for _, t := range []string{"shopping_list", "recipe_ingredients", "meal_history_ingredients", "stock_lots", "product_skus"} {
		fr, err := tx.Query(`PRAGMA foreign_key_check(` + t + `)`)
		if err != nil {
			return err
		}
		n := 0
		for fr.Next() {
			n++
		}
		fr.Close()
		if n > 0 {
			log.Printf("db: %d pre-existing foreign key violations in %s (left as is)", n, t)
		}
	}
	return nil
}
