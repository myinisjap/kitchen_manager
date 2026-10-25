package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"kitchen_manager/internal/auth"
	"kitchen_manager/services"
	"kitchen_manager/units"
)

func RegisterInventory(mux *http.ServeMux, db *sql.DB, hub ...*Hub) {
	var wsHub *Hub
	if len(hub) > 0 {
		wsHub = hub[0]
	}
	broadcastInventory := func() {
		if wsHub != nil {
			wsHub.Broadcast("inventory_updated")
		}
	}
	mux.HandleFunc("GET /api/units", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, units.ByDimension())
	})

	mux.HandleFunc("POST /api/inventory/", func(w http.ResponseWriter, r *http.Request) {
		var item struct {
			Name            string  `json:"name"`
			Quantity        float64 `json:"quantity"`
			Unit            string  `json:"unit"`
			PreferredUnit   string  `json:"preferred_unit"`
			Location        string  `json:"location"`
			ExpirationDate  string  `json:"expiration_date"`
			LowThreshold    float64 `json:"low_threshold"`
			Barcode         string  `json:"barcode"`
			UnitCostCents   int64   `json:"unit_cost_cents"`
			QuantityPerScan float64 `json:"quantity_per_scan"`
		}
		if err := ReadJSON(r, &item); err != nil || item.Name == "" {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if item.Quantity < 0 {
			WriteError(w, http.StatusBadRequest, "quantity must be non-negative")
			return
		}
		if item.LowThreshold < 0 {
			WriteError(w, http.StatusBadRequest, "low_threshold must be non-negative")
			return
		}
		if item.ExpirationDate != "" {
			if _, err := time.Parse("2006-01-02", item.ExpirationDate); err != nil {
				WriteError(w, http.StatusBadRequest, "invalid expiration_date format, expected YYYY-MM-DD")
				return
			}
		}
		if item.Unit != "" && !units.IsValid(units.Unit(item.Unit)) {
			WriteError(w, http.StatusBadRequest, "invalid unit; valid units: "+units.ValidList())
			return
		}
		if item.PreferredUnit != "" {
			if !units.IsValid(units.Unit(item.PreferredUnit)) {
				WriteError(w, http.StatusBadRequest, "invalid preferred_unit; valid units: "+units.ValidList())
				return
			}
			if item.Unit != "" && units.BaseDimension(units.Unit(item.Unit)) != units.BaseDimension(units.Unit(item.PreferredUnit)) {
				WriteError(w, http.StatusBadRequest, "preferred_unit must be in the same dimension as unit")
				return
			}
		}
		if item.QuantityPerScan < 0 {
			item.QuantityPerScan = 0
		}
		item.Name = strings.TrimSpace(item.Name)

		// Same product + unit + location + expiry (and a barcode, so unrelated
		// unlabelled items aren't merged by accident) adds to the existing lot.
		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback() // no-op after Commit
		changedBy := auth.EmailFromContext(r.Context())

		// Resolve the product: a known barcode (primary or SKU alias) wins,
		// otherwise the name.
		var prod product
		byBarcode, viaSKU, created := false, false, false
		if item.Barcode != "" {
			pid, _, skuID, matched, err := resolveBarcode(tx, item.Barcode)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			if matched {
				if prod, err = getProduct(tx, pid); err != nil {
					WriteServerError(w, r, err)
					return
				}
				byBarcode, viaSKU = true, skuID != 0
			}
		}
		if !byBarcode {
			prod, created, err = getOrCreateProduct(tx, item.Name, item.Unit, item.PreferredUnit)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
		}

		lotUnit, lotQty := item.Unit, item.Quantity
		if byBarcode {
			// Express the incoming quantity in the product's unit when possible.
			eff := prod.EffUnit()
			if lotUnit == "" {
				lotUnit = eff
			} else if eff != "" && lotUnit != eff {
				if converted, convErr := units.Convert(lotQty, units.Unit(lotUnit), units.Unit(eff)); convErr == nil {
					lotQty, lotUnit = converted, eff
				}
			}
		}

		if created && item.Barcode != "" {
			qps := item.QuantityPerScan
			if qps <= 0 {
				qps = 1
			}
			if _, err := tx.Exec(`UPDATE products SET barcode=?, quantity_per_scan=? WHERE id=?`, item.Barcode, qps, prod.ID); err != nil {
				WriteServerError(w, r, err)
				return
			}
		} else if !byBarcode && item.Barcode != "" {
			// Known product, new barcode: keep it scannable.
			qps := item.QuantityPerScan
			if qps <= 0 {
				qps = 1
			}
			if prod.Barcode == "" {
				_, err = tx.Exec(`UPDATE products SET barcode=?, quantity_per_scan=? WHERE id=?`, item.Barcode, qps, prod.ID)
			} else {
				_, err = tx.Exec(`INSERT INTO product_skus (product_id,barcode,quantity_per_scan) VALUES (?,?,?)`, prod.ID, item.Barcode, qps)
			}
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		if item.PreferredUnit != "" && prod.PreferredUnit == "" && !created {
			if _, err := tx.Exec(`UPDATE products SET preferred_unit=? WHERE id=?`, item.PreferredUnit, prod.ID); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		if item.LowThreshold > 0 {
			if err := setProductPar(tx, prod.ID, item.LowThreshold); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}

		var lotID int64
		if item.Barcode != "" {
			err = tx.QueryRow(
				`SELECT id FROM stock_lots WHERE product_id=? AND unit=? AND location=? AND expiration_date=? ORDER BY id LIMIT 1`,
				prod.ID, lotUnit, item.Location, item.ExpirationDate,
			).Scan(&lotID)
			if err != nil && err != sql.ErrNoRows {
				WriteServerError(w, r, err)
				return
			}
		}
		source := "manual"
		if viaSKU {
			source = "receipt_merge"
		}
		if lotID != 0 {
			// Duplicate found: atomically add quantity to the existing lot.
			before, after, err := addStock(tx, lotID, lotQty)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			if err := LogHistory(db, tx, LogHistoryParams{
				InventoryID: lotID, ProductID: prod.ID, ItemName: prod.Name, ChangeType: "add",
				QuantityBefore: &before, QuantityAfter: &after, ChangedBy: changedBy, Unit: lotUnit, Source: source,
			}); err != nil {
				WriteServerError(w, r, err)
				return
			}
			updated, err := getLot(tx, lotID)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			if err := tx.Commit(); err != nil {
				WriteServerError(w, r, err)
				return
			}
			WriteJSON(w, http.StatusOK, updated)
			broadcastInventory()
			return
		}

		res, err := tx.Exec(`INSERT INTO stock_lots (product_id,quantity,unit,location,expiration_date,unit_cost_cents) VALUES (?,?,?,?,?,?)`,
			prod.ID, lotQty, lotUnit, item.Location, item.ExpirationDate, item.UnitCostCents)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		lotID, _ = res.LastInsertId()
		zeroQty := 0.0
		if err := LogHistory(db, tx, LogHistoryParams{
			InventoryID: lotID, ProductID: prod.ID, ItemName: prod.Name, ChangeType: "add",
			QuantityBefore: &zeroQty, QuantityAfter: &lotQty, Unit: lotUnit, Source: source, ChangedBy: changedBy,
		}); err != nil {
			WriteServerError(w, r, err)
			return
		}
		created2, err := getLot(tx, lotID)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, created2)
		broadcastInventory()
	})

	mux.HandleFunc("GET /api/inventory/expiring", func(w http.ResponseWriter, r *http.Request) {
		_, cutoff := expiryDaysAndCutoff(r, db)
		items, err := expiringLots(db, cutoff)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, items)
	})

	mux.HandleFunc("GET /api/inventory/suggestions", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		query := `SELECT p.name, p.unit, p.preferred_unit, p.par_level,
			COALESCE((SELECT l.location FROM stock_lots l JOIN inventory_history h ON h.inventory_id = l.id WHERE l.product_id = p.id ORDER BY h.changed_at DESC LIMIT 1),
			         (SELECT MAX(location) FROM stock_lots WHERE product_id = p.id)) AS location
			FROM products p`
		var args []any
		if q != "" {
			query += ` WHERE p.name LIKE ?`
			args = append(args, q+"%")
		}
		query += ` ORDER BY p.name COLLATE NOCASE LIMIT 10`
		rows, err := db.Query(query, args...)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer rows.Close()
		suggestions := []map[string]any{}
		for rows.Next() {
			var name, unit, preferredUnit string
			var lowThreshold float64
			var location sql.NullString
			if err := rows.Scan(&name, &unit, &preferredUnit, &lowThreshold, &location); err != nil {
				WriteServerError(w, r, err)
				return
			}
			suggestions = append(suggestions, map[string]any{
				"name":           name,
				"unit":           unit,
				"preferred_unit": preferredUnit,
				"location":       location.String,
				"low_threshold":  lowThreshold,
			})
		}
		if err := rows.Err(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, suggestions)
	})

	// A barcode resolves to a product. The response is the product's
	// recommended lot (earliest expiry with stock); a product with no stock
	// left comes back as a lot-shaped stub with id 0 so the client can add a
	// new lot to it.
	mux.HandleFunc("GET /api/inventory/barcode/{code}", func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		if code == "" {
			WriteError(w, http.StatusBadRequest, "missing barcode")
			return
		}
		pid, qps, skuID, matched, err := resolveBarcode(db, code)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if !matched {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		lotID, err := recommendedLotID(db, pid)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		var item Lot
		if lotID != 0 {
			item, err = getLot(db, lotID)
		} else {
			item, err = productStub(db, pid)
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if skuID != 0 {
			item.QuantityPerScan = qps
			item.SKUID = skuID
			item.MatchedBarcode = code
		}
		WriteJSON(w, http.StatusOK, item)
	})

	mux.HandleFunc("GET /api/inventory/similar", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if name == "" {
			WriteJSON(w, http.StatusOK, []map[string]any{})
			return
		}
		// Build per-word LIKE conditions so "creamy peanut butter" matches "peanut butter"
		words := strings.Fields(strings.ToLower(name))
		if len(words) == 0 {
			WriteJSON(w, http.StatusOK, []map[string]any{})
			return
		}
		conds := make([]string, len(words))
		args := make([]any, len(words))
		for i, w := range words {
			conds[i] = "p.name LIKE ?"
			args[i] = "%" + w + "%"
		}
		// One candidate per product, identified by a lot (the SKU endpoints
		// take a lot id). Products with no stock left can't be offered.
		q := `SELECT l.id, p.name, l.quantity, l.unit, p.preferred_unit, l.location, p.id
			FROM products p JOIN stock_lots l ON l.id = (
				SELECT id FROM stock_lots WHERE product_id = p.id
				ORDER BY quantity>0 DESC, CASE WHEN expiration_date='' THEN 1 ELSE 0 END, expiration_date, id DESC LIMIT 1)
			WHERE ` + strings.Join(conds, " AND ") + ` ORDER BY p.name COLLATE NOCASE LIMIT 3`
		rows, err := db.Query(q, args...)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer rows.Close()
		candidates := []map[string]any{}
		for rows.Next() {
			var id, pid int64
			var n, unit, preferredUnit, location string
			var qty float64
			if err := rows.Scan(&id, &n, &qty, &unit, &preferredUnit, &location, &pid); err != nil {
				WriteServerError(w, r, err)
				return
			}
			candidates = append(candidates, map[string]any{
				"id": id, "product_id": pid, "name": n, "quantity": qty,
				"unit": unit, "preferred_unit": preferredUnit, "location": location,
			})
		}
		if err := rows.Err(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, candidates)
	})

	// One group per product. Products with no lots appear as out_of_stock
	// groups with empty locations (unless a location filter is set).
	mux.HandleFunc("GET /api/inventory/grouped", func(w http.ResponseWriter, r *http.Request) {
		locFilter := r.URL.Query().Get("location")
		nameFilter := r.URL.Query().Get("name")

		q := `SELECT l.id, l.product_id, l.location, l.quantity, l.unit, l.expiration_date, p.name, p.unit, p.preferred_unit, p.par_level, p.barcode
			FROM stock_lots l JOIN products p ON p.id = l.product_id WHERE 1=1`
		args := []any{}
		if locFilter != "" {
			q += ` AND l.location=?`
			args = append(args, locFilter)
		}
		if nameFilter != "" {
			q += ` AND p.name LIKE ?`
			args = append(args, "%"+nameFilter+"%")
		}
		q += ` ORDER BY p.name COLLATE NOCASE, p.id, CASE WHEN l.expiration_date='' THEN 1 ELSE 0 END, l.expiration_date, l.id`

		rows, err := db.Query(q, args...)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer rows.Close()

		type locationEntry struct {
			ID             int64   `json:"id"`
			Location       string  `json:"location"`
			Quantity       float64 `json:"quantity"`
			Unit           string  `json:"unit"`
			UnitMismatch   bool    `json:"unit_mismatch"`
			ExpirationDate string  `json:"expiration_date"`
			Barcode        string  `json:"barcode"`
		}

		type groupedItem struct {
			ProductID             int64           `json:"product_id"`
			Name                  string          `json:"name"`
			Unit                  string          `json:"unit"`
			PreferredUnit         string          `json:"preferred_unit"`
			TotalQuantity         float64         `json:"total_quantity"`
			LowThreshold          float64         `json:"low_threshold"`
			ParLevel              float64         `json:"par_level"`
			ParUnit               string          `json:"par_unit"`
			ProductTotal          float64         `json:"product_total"`
			BelowPar              bool            `json:"below_par"`
			Locations             []locationEntry `json:"locations"`
			RecommendedLocationID int64           `json:"recommended_location_id"`
			OutOfStock            bool            `json:"out_of_stock"`
			Barcode               string          `json:"barcode"`
		}

		results := []groupedItem{}
		byProduct := map[int64]int{}
		for rows.Next() {
			var e locationEntry
			var pid int64
			var name, pUnit, pref, pbar string
			var par float64
			if err := rows.Scan(&e.ID, &pid, &e.Location, &e.Quantity, &e.Unit, &e.ExpirationDate, &name, &pUnit, &pref, &par, &pbar); err != nil {
				WriteServerError(w, r, err)
				return
			}
			e.Barcode = pbar
			idx, ok := byProduct[pid]
			if !ok {
				eff := pref
				if eff == "" {
					eff = pUnit
				}
				results = append(results, groupedItem{
					ProductID: pid, Name: name, Unit: eff, PreferredUnit: pref, LowThreshold: par,
					Locations: []locationEntry{}, RecommendedLocationID: e.ID, Barcode: pbar,
				})
				idx = len(results) - 1
				byProduct[pid] = idx
			}
			g := &results[idx]
			if c, err := units.Convert(e.Quantity, units.Unit(e.Unit), units.Unit(g.Unit)); err == nil {
				g.TotalQuantity += c
			} else {
				e.UnitMismatch = true
			}
			g.Locations = append(g.Locations, e)
		}
		if err := rows.Err(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		rows.Close()
		if locFilter == "" {
			oq := `SELECT id, name, unit, preferred_unit, par_level, barcode FROM products p
				WHERE NOT EXISTS (SELECT 1 FROM stock_lots l WHERE l.product_id = p.id)`
			oargs := []any{}
			if nameFilter != "" {
				oq += ` AND p.name LIKE ?`
				oargs = append(oargs, "%"+nameFilter+"%")
			}
			orows, err := db.Query(oq, oargs...)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			for orows.Next() {
				var pid int64
				var name, pUnit, pref, bar string
				var par float64
				if err := orows.Scan(&pid, &name, &pUnit, &pref, &par, &bar); err != nil {
					orows.Close()
					WriteServerError(w, r, err)
					return
				}
				eff := pref
				if eff == "" {
					eff = pUnit
				}
				results = append(results, groupedItem{
					ProductID: pid, Name: name, Unit: eff, PreferredUnit: pref, LowThreshold: par,
					Barcode: bar, Locations: []locationEntry{}, OutOfStock: true,
				})
			}
			err = orows.Err()
			orows.Close()
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			sort.SliceStable(results, func(i, j int) bool {
				a, b := strings.ToLower(results[i].Name), strings.ToLower(results[j].Name)
				if a != b {
					return a < b
				}
				return results[i].ProductID < results[j].ProductID
			})
		}
		levels, err := services.ProductLevels(db)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		byID := make(map[int64]services.ProductLevel, len(levels))
		for _, l := range levels {
			byID[l.ID] = l
		}
		for i := range results {
			if l, ok := byID[results[i].ProductID]; ok {
				results[i].ParLevel = l.Par
				results[i].ParUnit = l.Unit
				results[i].ProductTotal = l.Total
				results[i].BelowPar = l.BelowPar()
			}
		}
		WriteJSON(w, http.StatusOK, results)
	})

	mux.HandleFunc("GET /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		item, err := getLot(db, id)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, item)
	})

	mux.HandleFunc("GET /api/inventory/", func(w http.ResponseWriter, r *http.Request) {
		items, err := listLots(db, lotFilter{Name: r.URL.Query().Get("name"), Location: r.URL.Query().Get("location")})
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, items)
	})

	mux.HandleFunc("PATCH /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var p inventoryPatch
		if err := ReadJSON(r, &p); err != nil {
			var ute *json.UnmarshalTypeError
			if errors.As(err, &ute) {
				WriteError(w, http.StatusBadRequest, "invalid type for field "+ute.Field)
				return
			}
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		unitMsg := "valid units: " + units.ValidList()
		if p.ExpirationDate != nil && *p.ExpirationDate != "" {
			if _, err := time.Parse("2006-01-02", *p.ExpirationDate); err != nil {
				WriteError(w, http.StatusBadRequest, "invalid expiration_date format, expected YYYY-MM-DD")
				return
			}
		}
		if p.Quantity != nil && *p.Quantity < 0 {
			WriteError(w, http.StatusBadRequest, "quantity cannot be negative")
			return
		}
		if p.LowThreshold != nil && *p.LowThreshold < 0 {
			WriteError(w, http.StatusBadRequest, "low_threshold cannot be negative")
			return
		}
		if p.Unit != nil && *p.Unit != "" && !units.IsValid(units.Unit(*p.Unit)) {
			WriteError(w, http.StatusBadRequest, "invalid unit; "+unitMsg)
			return
		}
		if p.PreferredUnit != nil && *p.PreferredUnit != "" && !units.IsValid(units.Unit(*p.PreferredUnit)) {
			WriteError(w, http.StatusBadRequest, "invalid preferred_unit; "+unitMsg)
			return
		}
		if p.Name != nil {
			n := strings.TrimSpace(*p.Name)
			p.Name = &n
		}

		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback()

		var qtyBefore float64
		var unitBefore string
		var productID int64
		core, err := getLotCore(tx, id)
		qtyBefore, unitBefore, productID = core.Quantity, core.Unit, core.ProductID
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		prod, err := getProduct(tx, productID)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		nameBefore := prod.Name
		finalUnit := unitBefore
		if p.Unit != nil {
			finalUnit = *p.Unit
		}
		if p.PreferredUnit != nil && *p.PreferredUnit != "" && finalUnit != "" &&
			units.BaseDimension(units.Unit(finalUnit)) != units.BaseDimension(units.Unit(*p.PreferredUnit)) {
			WriteError(w, http.StatusBadRequest, "preferred_unit must be in the same dimension as unit")
			return
		}

		// Renaming a lot re-homes it: onto the product that already has that
		// name, or (if it is the product's only lot) renames the product,
		// otherwise onto a new product that inherits par and preferred unit.
		if p.Name != nil && *p.Name != "" && !strings.EqualFold(*p.Name, prod.Name) {
			other, found, err := findProductByName(tx, *p.Name)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			n, err := lotCount(tx, prod.ID)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			switch {
			case found && other.ID != prod.ID:
				if _, err := tx.Exec(`UPDATE stock_lots SET product_id=? WHERE id=?`, other.ID, id); err != nil {
					WriteServerError(w, r, err)
					return
				}
				if n == 1 {
					// The old product has no lots left: fold it into the target.
					if err := mergeProductInto(tx, prod.ID, other.ID); err != nil {
						WriteServerError(w, r, err)
						return
					}
				}
				prod = other
			case n == 1:
				if _, err := tx.Exec(`UPDATE products SET name=? WHERE id=?`, *p.Name, prod.ID); err != nil {
					WriteServerError(w, r, err)
					return
				}
				prod.Name = *p.Name
			default:
				res, err := tx.Exec(`INSERT INTO products (name,unit,preferred_unit,par_level) VALUES (?,?,?,?)`, *p.Name, unitBefore, prod.PreferredUnit, prod.Par)
				if err != nil {
					WriteServerError(w, r, err)
					return
				}
				newID, _ := res.LastInsertId()
				if _, err := tx.Exec(`UPDATE stock_lots SET product_id=? WHERE id=?`, newID, id); err != nil {
					WriteServerError(w, r, err)
					return
				}
				if prod, err = getProduct(tx, newID); err != nil {
					WriteServerError(w, r, err)
					return
				}
			}
		} else if p.Name != nil && *p.Name != "" && *p.Name != prod.Name {
			// Case-only rename of the product.
			if _, err := tx.Exec(`UPDATE products SET name=? WHERE id=?`, *p.Name, prod.ID); err != nil {
				WriteServerError(w, r, err)
				return
			}
			prod.Name = *p.Name
		}

		// Lot fields.
		var sets []string
		var args []any
		add := func(col string, v any) {
			sets = append(sets, col+"=?")
			args = append(args, v)
		}
		if p.Quantity != nil {
			add("quantity", *p.Quantity)
		}
		if p.Unit != nil {
			add("unit", *p.Unit)
		}
		if p.Location != nil {
			add("location", *p.Location)
		}
		if p.ExpirationDate != nil {
			add("expiration_date", *p.ExpirationDate)
		}
		if p.UnitCostCents != nil {
			add("unit_cost_cents", *p.UnitCostCents)
		}
		if len(sets) > 0 {
			args = append(args, id)
			if _, err := tx.Exec(`UPDATE stock_lots SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}

		// Product fields.
		if p.Unit != nil {
			if n, err := lotCount(tx, prod.ID); err == nil && n == 1 {
				if _, err := tx.Exec(`UPDATE products SET unit=? WHERE id=?`, *p.Unit, prod.ID); err != nil {
					WriteServerError(w, r, err)
					return
				}
			}
		}
		if p.LowThreshold != nil {
			if err := setProductPar(tx, prod.ID, *p.LowThreshold); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		if p.PreferredUnit != nil {
			if _, err := tx.Exec(`UPDATE products SET preferred_unit=? WHERE id=?`, *p.PreferredUnit, prod.ID); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		if p.Barcode != nil && *p.Barcode != prod.Barcode {
			if *p.Barcode != "" {
				taken, err := barcodeTaken(tx, *p.Barcode, prod.ID)
				if err != nil {
					WriteServerError(w, r, err)
					return
				}
				if taken {
					WriteError(w, http.StatusConflict, "barcode already assigned to another item")
					return
				}
				// Drop an alias of this product that the new primary replaces.
				if _, err := tx.Exec(`DELETE FROM product_skus WHERE barcode=? AND product_id=?`, *p.Barcode, prod.ID); err != nil {
					WriteServerError(w, r, err)
					return
				}
			}
			if _, err := tx.Exec(`UPDATE products SET barcode=? WHERE id=?`, *p.Barcode, prod.ID); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		if p.QuantityPerScan != nil {
			if _, err := tx.Exec(`UPDATE products SET quantity_per_scan=? WHERE id=?`, *p.QuantityPerScan, prod.ID); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}

		if p.Quantity != nil {
			source := r.URL.Query().Get("source")
			if source == "" {
				source = "manual"
			}
			qtyAfter := *p.Quantity
			changeType := "edit"
			if qtyAfter > qtyBefore {
				changeType = "add"
			} else if qtyAfter < qtyBefore {
				changeType = "deduct"
			}
			if err := LogHistory(db, tx, LogHistoryParams{
				InventoryID:    id,
				ProductID:      prod.ID,
				ItemName:       nameBefore,
				ChangeType:     changeType,
				QuantityBefore: &qtyBefore,
				QuantityAfter:  &qtyAfter,
				Unit:           unitBefore,
				Source:         source,
				ChangedBy:      auth.EmailFromContext(r.Context()),
			}); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		if p.Quantity != nil && *p.Quantity == 0 && qtyBefore > 0 {
			if err := retireEmptyLots(tx, []int64{id}); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}

		item, err := getLot(db, id)
		if err == sql.ErrNoRows {
			// The lot ran out and was removed; answer with what is left of the product.
			var rec int64
			if rec, err = recommendedLotID(db, prod.ID); err == nil && rec != 0 {
				item, err = getLot(db, rec)
			} else if err == nil {
				stub, serr := productStub(db, prod.ID)
				if serr != nil {
					WriteJSON(w, http.StatusOK, lotFallback{ProductID: prod.ID, LotID: &id})
					broadcastInventory()
					return
				}
				stub.LotID = &id
				WriteJSON(w, http.StatusOK, stub)
				broadcastInventory()
				return
			}
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, item)
		broadcastInventory()
	})

	mux.HandleFunc("POST /api/inventory/{id}/deduct", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var body struct {
			Quantity  float64 `json:"quantity"`
			Unit      string  `json:"unit"`
			SingleLot bool    `json:"single_lot"`
		}
		if err := ReadJSON(r, &body); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if body.Quantity <= 0 {
			WriteError(w, http.StatusBadRequest, "quantity must be positive")
			return
		}
		if body.Unit != "" && !units.IsValid(units.Unit(body.Unit)) {
			WriteError(w, http.StatusBadRequest, "invalid unit")
			return
		}
		source := r.URL.Query().Get("source")
		if source == "" {
			source = "manual"
		}
		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback()
		dres, err := deductStock(db, tx, deductRequest{
			ItemID: id, Qty: body.Quantity, Unit: body.Unit, Source: source,
			ChangedBy: auth.EmailFromContext(r.Context()), SingleLot: body.SingleLot,
		})
		if err != nil {
			if errors.Is(err, errItemNotFound) {
				WriteError(w, http.StatusNotFound, err.Error())
			} else if errors.Is(err, errIncompatibleUnits) {
				WriteError(w, http.StatusUnprocessableEntity, err.Error())
			} else {
				WriteServerError(w, r, err)
			}
			return
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		deducted := dres.Deducted
		var out any = lotFallback{ID: id, ProductID: dres.ProductID, Deducted: &deducted}
		if dres.ResultItemID == 0 {
			if stub, serr := productStub(db, dres.ProductID); serr == nil {
				stub.LotID = &id
				stub.Deducted = &deducted
				out = stub
			}
		} else {
			item, err := getLot(db, dres.ResultItemID)
			if err == nil {
				item.Deducted = &deducted
				out = item
			} else if err != sql.ErrNoRows {
				WriteServerError(w, r, err)
				return
			}
		}
		WriteJSON(w, http.StatusOK, out)
		broadcastInventory()
		broadcastInventory()
	})

	// SKU endpoints take a stock lot id for compatibility and operate on the
	// lot's product.
	mux.HandleFunc("GET /api/skus/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		rows, err := db.Query(`SELECT s.id, s.product_id, s.barcode, s.quantity_per_scan FROM product_skus s
			WHERE s.product_id=(SELECT product_id FROM stock_lots WHERE id=?) ORDER BY s.id`, id)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer rows.Close()
		skus := []map[string]any{}
		for rows.Next() {
			var skuID, pid int64
			var barcode string
			var qps float64
			if err := rows.Scan(&skuID, &pid, &barcode, &qps); err != nil {
				WriteServerError(w, r, err)
				return
			}
			skus = append(skus, map[string]any{"id": skuID, "inventory_id": id, "product_id": pid, "barcode": barcode, "quantity_per_scan": qps})
		}
		if err := rows.Err(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, skus)
	})

	mux.HandleFunc("POST /api/skus/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var body struct {
			Barcode         string  `json:"barcode"`
			QuantityPerScan float64 `json:"quantity_per_scan"`
			Quantity        float64 `json:"quantity"`
			Unit            string  `json:"unit"`
		}
		if err := ReadJSON(r, &body); err != nil || body.Barcode == "" {
			WriteError(w, http.StatusBadRequest, "barcode is required")
			return
		}

		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback() // no-op after Commit
		productID, err := productOfLot(tx, id)
		if err == errItemNotFound {
			WriteError(w, http.StatusNotFound, "inventory item not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		prod, err := getProduct(tx, productID)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}

		// Check barcode not already used as a primary barcode or SKU alias.
		var collision int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM products WHERE barcode=?`, body.Barcode).Scan(&collision); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if collision > 0 {
			WriteError(w, http.StatusConflict, "barcode already assigned to an inventory item")
			return
		}
		if err := tx.QueryRow(`SELECT COUNT(*) FROM product_skus WHERE barcode=?`, body.Barcode).Scan(&collision); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if collision > 0 {
			WriteError(w, http.StatusConflict, "barcode already exists as a SKU alias")
			return
		}

		// Quantities are added to the lot in the product's unit.
		targetUnit := prod.EffUnit()
		addQty := body.Quantity
		if body.Unit != "" && targetUnit != "" && body.Unit != targetUnit {
			fromDim := units.BaseDimension(units.Unit(body.Unit))
			toDim := units.BaseDimension(units.Unit(targetUnit))
			if fromDim == "" || toDim == "" || fromDim != toDim {
				WriteError(w, http.StatusBadRequest, "unit dimension mismatch: cannot add "+body.Unit+" to item tracked in "+targetUnit)
				return
			}
			converted, err := units.Convert(body.Quantity, units.Unit(body.Unit), units.Unit(targetUnit))
			if err != nil {
				WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			addQty = converted
		}

		res, err := tx.Exec(`INSERT INTO product_skus (product_id, barcode, quantity_per_scan) VALUES (?,?,?)`, productID, body.Barcode, body.QuantityPerScan)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		skuID, _ := res.LastInsertId()

		before, after, err := addStock(tx, id, addQty)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if addQty != 0 {
			if err := LogHistory(db, tx, LogHistoryParams{
				InventoryID:    id,
				ProductID:      productID,
				ItemName:       prod.Name,
				ChangeType:     "add",
				QuantityBefore: &before,
				QuantityAfter:  &after,
				Unit:           targetUnit,
				Source:         "barcode_merge",
				ChangedBy:      auth.EmailFromContext(r.Context()),
			}); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}

		item, err := getLot(tx, id)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]any{
			"item": item,
			"sku":  map[string]any{"id": skuID, "inventory_id": id, "product_id": productID, "barcode": body.Barcode, "quantity_per_scan": body.QuantityPerScan},
		})
		broadcastInventory()
	})

	mux.HandleFunc("DELETE /api/skus/{sku_id}", func(w http.ResponseWriter, r *http.Request) {
		skuID, ok := pathIDFromPattern(r, "sku_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid sku_id")
			return
		}
		res, err := db.Exec(`DELETE FROM product_skus WHERE id=?`, skuID)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "sku not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/inventory/{id}/merge-into/{target_id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		targetID, err := strconv.ParseInt(r.PathValue("target_id"), 10, 64)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid target_id")
			return
		}
		if id == targetID {
			WriteError(w, http.StatusBadRequest, "source and target must be different items")
			return
		}

		// Optional body: override quantity/unit when dimensions differ
		var body struct {
			Quantity float64 `json:"quantity"`
			Unit     string  `json:"unit"`
		}
		_ = ReadJSON(r, &body)

		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback() // no-op after Commit

		var srcUnit string
		var srcQty float64
		var srcProduct int64
		srcCore, err := getLotCore(tx, id)
		srcQty, srcUnit, srcProduct = srcCore.Quantity, srcCore.Unit, srcCore.ProductID
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "source item not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		var tgtUnit string
		var tgtProduct int64
		tgtCore, err := getLotCore(tx, targetID)
		tgtUnit, tgtProduct = tgtCore.Unit, tgtCore.ProductID
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "target item not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		tgt, err := getProduct(tx, tgtProduct)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}

		// Quantity to add, expressed in the target lot's unit.
		addQty := srcQty
		addUnit := srcUnit
		if body.Unit != "" {
			// Caller supplied override (unit conflict resolution)
			addQty = body.Quantity
			addUnit = body.Unit
		}
		if addUnit != "" && tgtUnit != "" && addUnit != tgtUnit {
			fromDim := units.BaseDimension(units.Unit(addUnit))
			toDim := units.BaseDimension(units.Unit(tgtUnit))
			if fromDim == "" || toDim == "" || fromDim != toDim {
				WriteError(w, http.StatusBadRequest, "unit dimension mismatch: cannot merge "+addUnit+" into item tracked in "+tgtUnit)
				return
			}
			converted, err := units.Convert(addQty, units.Unit(addUnit), units.Unit(tgtUnit))
			if err != nil {
				WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			addQty = converted
		}

		tgtBefore, newTgtQty, err := addStock(tx, targetID, addQty)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := deleteLot(tx, id); err != nil {
			WriteServerError(w, r, err)
			return
		}
		// Merging the last lot of another product folds that whole product
		// (links, barcodes, par) into the target's product.
		if srcProduct != tgtProduct {
			n, err := lotCount(tx, srcProduct)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			if n == 0 {
				if err := mergeProductInto(tx, srcProduct, tgtProduct); err != nil {
					WriteServerError(w, r, err)
					return
				}
			}
		}

		if err := LogHistory(db, tx, LogHistoryParams{
			InventoryID:    targetID,
			ProductID:      tgtProduct,
			ItemName:       tgt.Name,
			ChangeType:     "add",
			QuantityBefore: &tgtBefore,
			QuantityAfter:  &newTgtQty,
			Unit:           tgtUnit,
			Source:         "item_merge",
			ChangedBy:      auth.EmailFromContext(r.Context()),
		}); err != nil {
			WriteServerError(w, r, err)
			return
		}
		item, err := getLot(tx, targetID)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, item)
		broadcastInventory()
	})

	// Deleting the last lot of a product removes the product too (its links
	// are cleared by the foreign keys). Deleting one of several lots keeps the
	// product and everything pointing at it.
	mux.HandleFunc("DELETE /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback()
		var itemName, itemUnit string
		var itemQty float64
		var productID int64
		err = tx.QueryRow(`SELECT p.name, l.quantity, l.unit, l.product_id FROM stock_lots l JOIN products p ON p.id=l.product_id WHERE l.id=?`, id).
			Scan(&itemName, &itemQty, &itemUnit, &productID)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := deleteLot(tx, id); err != nil {
			WriteServerError(w, r, err)
			return
		}
		n, err := lotCount(tx, productID)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if n == 0 {
			if _, err := tx.Exec(`DELETE FROM products WHERE id=?`, productID); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		if err := LogHistory(db, tx, LogHistoryParams{
			InventoryID:    id,
			ProductID:      productID,
			ItemName:       itemName,
			ChangeType:     "delete",
			QuantityBefore: &itemQty,
			QuantityAfter:  nil,
			Unit:           itemUnit,
			Source:         "manual",
			ChangedBy:      auth.EmailFromContext(r.Context()),
		}); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		broadcastInventory()
	})
}

// pathIDFromPattern reads a named path parameter from Go 1.22 ServeMux patterns.
func pathIDFromPattern(r *http.Request, param string) (int64, bool) {
	val := r.PathValue(param)
	id, err := strconv.ParseInt(val, 10, 64)
	return id, err == nil
}

// inventoryPatch: quantity/unit/location/expiration_date/unit_cost_cents edit
// the lot; the rest edit its product.
type inventoryPatch struct {
	Name            *string  `json:"name"`
	Quantity        *float64 `json:"quantity"`
	Unit            *string  `json:"unit"`
	Location        *string  `json:"location"`
	ExpirationDate  *string  `json:"expiration_date"`
	LowThreshold    *float64 `json:"low_threshold"`
	Barcode         *string  `json:"barcode"`
	PreferredUnit   *string  `json:"preferred_unit"`
	UnitCostCents   *int64   `json:"unit_cost_cents"`
	QuantityPerScan *float64 `json:"quantity_per_scan"`
}
