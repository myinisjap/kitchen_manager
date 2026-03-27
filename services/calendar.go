package services

import (
	"database/sql"
	"fmt"
	"time"
)

// ShoppingNeed represents an ingredient that needs to be purchased.
type ShoppingNeed struct {
	InventoryID    *int64
	Name           string
	Unit           string
	QuantityNeeded float64
}

// GenerateWeeklyShopping simulates daily inventory depletion over a 7-day week
// and returns the shopping items needed, accounting for prior days' usage.
func GenerateWeeklyShopping(db *sql.DB, weekStart string) ([]ShoppingNeed, error) {
	start, err := time.Parse("2006-01-02", weekStart)
	if err != nil {
		return nil, fmt.Errorf("invalid week_start date: %w", err)
	}
	end := start.AddDate(0, 0, 6).Format("2006-01-02")

	// Step 1: collect all calendar entries for the week, sorted by date
	type calEntry struct {
		recipeID       int64
		servings       int
		recipeServings int
	}
	rows, err := db.Query(`
		SELECT mc.recipe_id, mc.servings, r.servings
		FROM meal_calendar mc
		JOIN recipes r ON r.id = mc.recipe_id
		WHERE mc.date >= ? AND mc.date <= ?
		ORDER BY mc.date`, weekStart, end)
	if err != nil {
		return nil, err
	}
	var entries []calEntry
	for rows.Next() {
		var e calEntry
		if err := rows.Scan(&e.recipeID, &e.servings, &e.recipeServings); err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// Step 2: load current inventory into simulated map
	invRows, err := db.Query(`SELECT id, quantity FROM inventory`)
	if err != nil {
		return nil, err
	}
	simulated := map[int64]float64{}
	for invRows.Next() {
		var id int64
		var qty float64
		if err := invRows.Scan(&id, &qty); err != nil {
			invRows.Close()
			return nil, err
		}
		simulated[id] = qty
	}
	if err := invRows.Err(); err != nil {
		invRows.Close()
		return nil, err
	}
	invRows.Close()

	// Step 3: simulate day-by-day depletion
	// key: "invID|unit" or "name|unit" for unlinked ingredients
	needs := map[string]*ShoppingNeed{}

	for _, e := range entries {
		recServings := e.recipeServings
		if recServings == 0 {
			recServings = 1
		}
		scale := float64(e.servings) / float64(recServings)

		// Fetch ingredients for this recipe
		ingRows, err := db.Query(`SELECT inventory_id, name, quantity, unit FROM recipe_ingredients WHERE recipe_id=?`, e.recipeID)
		if err != nil {
			return nil, err
		}
		type ingRow struct {
			invID sql.NullInt64
			name  string
			qty   float64
			unit  string
		}
		var ings []ingRow
		for ingRows.Next() {
			var ing ingRow
			if err := ingRows.Scan(&ing.invID, &ing.name, &ing.qty, &ing.unit); err != nil {
				ingRows.Close()
				return nil, err
			}
			ings = append(ings, ing)
		}
		if err := ingRows.Err(); err != nil {
			ingRows.Close()
			return nil, err
		}
		ingRows.Close()

		for _, ing := range ings {
			needed := ing.qty * scale
			available := 0.0
			if ing.invID.Valid {
				available = simulated[ing.invID.Int64]
			}
			shortfall := needed - available
			if shortfall < 0 {
				shortfall = 0
			}

			key := ing.name + "|" + ing.unit
			if ing.invID.Valid {
				key = fmt.Sprintf("%d|%s", ing.invID.Int64, ing.unit)
			}

			if shortfall > 0 {
				if needs[key] == nil {
					var invIDPtr *int64
					if ing.invID.Valid {
						v := ing.invID.Int64
						invIDPtr = &v
					}
					needs[key] = &ShoppingNeed{
						InventoryID: invIDPtr,
						Name:        ing.name,
						Unit:        ing.unit,
					}
				}
				needs[key].QuantityNeeded += shortfall
			}

			// Deduct from simulated inventory for future days
			if ing.invID.Valid {
				remaining := available - needed
				if remaining < 0 {
					remaining = 0
				}
				simulated[ing.invID.Int64] = remaining
			}
		}
	}

	var result []ShoppingNeed
	for _, n := range needs {
		result = append(result, *n)
	}
	return result, nil
}
