package services

import (
	"database/sql"
	"fmt"
	"time"

	"kitchen_manager/units"
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
// Quantities are expressed in each inventory item's preferred_unit (or stored unit if no preference).
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

	// Step 2: load current inventory into simulated map (in preferred unit)
	type invInfo struct {
		qty           float64
		unit          string
		preferredUnit string // canonical display/storage unit; falls back to unit if empty
	}
	invRows, err := db.Query(`SELECT id, quantity, unit, preferred_unit FROM inventory`)
	if err != nil {
		return nil, err
	}
	inventory := map[int64]invInfo{}
	simulated := map[int64]float64{}
	for invRows.Next() {
		var id int64
		var qty float64
		var u, pu string
		if err := invRows.Scan(&id, &qty, &u, &pu); err != nil {
			invRows.Close()
			return nil, err
		}
		if pu == "" {
			pu = u
		}
		inventory[id] = invInfo{qty: qty, unit: u, preferredUnit: pu}
		simulated[id] = qty
	}
	if err := invRows.Err(); err != nil {
		invRows.Close()
		return nil, err
	}
	invRows.Close()

	// Step 3: simulate day-by-day depletion
	// key: "invID|preferredUnit" for linked ingredients, "name|unit" for unlinked
	needs := map[string]*ShoppingNeed{}

	for _, e := range entries {
		recServings := e.recipeServings
		if recServings == 0 {
			recServings = 1
		}
		scale := float64(e.servings) / float64(recServings)

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
			rawNeeded := ing.qty * scale
			ingUnit := units.Unit(ing.unit)

			if ing.invID.Valid {
				inv, ok := inventory[ing.invID.Int64]
				if !ok {
					continue
				}
				targetUnit := units.Unit(inv.preferredUnit)

				// Convert ingredient quantity to the inventory item's preferred unit
				needed := rawNeeded
				if converted, err := units.Convert(rawNeeded, ingUnit, targetUnit); err == nil {
					needed = converted
				}

				available := simulated[ing.invID.Int64]
				shortfall := needed - available
				if shortfall < 0 {
					shortfall = 0
				}

				key := fmt.Sprintf("%d|%s", ing.invID.Int64, string(targetUnit))
				if shortfall > 0 {
					if needs[key] == nil {
						v := ing.invID.Int64
						needs[key] = &ShoppingNeed{
							InventoryID: &v,
							Name:        ing.name,
							Unit:        string(targetUnit),
						}
					}
					needs[key].QuantityNeeded += shortfall
				}

				// Deduct from simulated inventory in preferred unit
				remaining := available - needed
				if remaining < 0 {
					remaining = 0
				}
				simulated[ing.invID.Int64] = remaining
			} else {
				// Unlinked ingredient: no conversion, use as-is
				shortfall := rawNeeded
				if shortfall < 0 {
					shortfall = 0
				}
				key := ing.name + "|" + ing.unit
				if shortfall > 0 {
					if needs[key] == nil {
						needs[key] = &ShoppingNeed{
							Name: ing.name,
							Unit: ing.unit,
						}
					}
					needs[key].QuantityNeeded += shortfall
				}
			}
		}
	}

	var result []ShoppingNeed
	for _, n := range needs {
		result = append(result, *n)
	}
	return result, nil
}
