package services

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"kitchen_manager/units"
)

// ShoppingNeed represents an ingredient that needs to be purchased.
type ShoppingNeed struct {
	ProductID      *int64
	Name           string
	Unit           string
	QuantityNeeded float64
	Recipes        []string // distinct contributing recipe names, sorted
}

// GenerateWeeklyShopping sums ingredient needs for all uncooked calendar
// entries in the 7-day week starting weekStart and subtracts each product's
// current stock once, returning the shortfalls.
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
		recipeName     string
	}
	rows, err := db.Query(`
		SELECT mc.recipe_id, mc.servings, r.servings, r.name
		FROM meal_calendar mc
		JOIN recipes r ON r.id = mc.recipe_id
		WHERE mc.date >= ? AND mc.date <= ?
		AND NOT EXISTS (SELECT 1 FROM meal_history mh WHERE mh.calendar_entry_id = mc.id)
		ORDER BY mc.date`, weekStart, end)
	if err != nil {
		return nil, err
	}
	var entries []calEntry
	for rows.Next() {
		var e calEntry
		if err := rows.Scan(&e.recipeID, &e.servings, &e.recipeServings, &e.recipeName); err != nil {
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

	// Step 2: current stock per product, summed over all its lots and
	// expressed in the product's unit.
	levels, err := ProductLevels(db)
	if err != nil {
		return nil, err
	}
	type invInfo struct {
		unit string
	}
	inventory := map[int64]invInfo{}
	simulated := map[int64]float64{}
	for _, l := range levels {
		inventory[l.ID] = invInfo{unit: l.Unit}
		simulated[l.ID] = l.Total
	}

	// Step 3: sum all ingredient quantities across all recipes, then subtract inventory once.
	// key: "invID|preferredUnit" for linked ingredients, "name|unit" for unlinked
	type totalNeed struct {
		invID   *int64
		name    string
		unit    string
		total   float64
		recipes map[string]bool
		// incompatible: recipe unit can't convert to the item's unit; total is
		// in the recipe's unit and stock is not subtracted.
		incompatible bool
	}
	totals := map[string]*totalNeed{}

	for _, e := range entries {
		scale := units.ServingsScale(e.servings, e.recipeServings)

		ingRows, err := db.Query(`SELECT product_id, name, quantity, unit FROM recipe_ingredients WHERE recipe_id=?`, e.recipeID)
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
				targetUnit := units.Unit(inv.unit)

				// Convert ingredient quantity to the product unit
				// If units can't be converted, file the need in the recipe's own
				// unit under a separate key and don't subtract stock.
				needed := rawNeeded
				key := fmt.Sprintf("%d|%s", ing.invID.Int64, string(targetUnit))
				unit := string(targetUnit)
				incompatible := false
				if converted, err := units.Convert(rawNeeded, ingUnit, targetUnit); err == nil {
					needed = converted
				} else {
					key = fmt.Sprintf("%d|%s|incompatible", ing.invID.Int64, ing.unit)
					unit = ing.unit
					incompatible = true
				}
				if totals[key] == nil {
					v := ing.invID.Int64
					totals[key] = &totalNeed{
						invID:        &v,
						name:         ing.name,
						unit:         unit,
						incompatible: incompatible,
						recipes:      map[string]bool{},
					}
				}
				totals[key].total += needed
				markRecipe(totals[key].recipes, e.recipeName)
			} else {
				// Unlinked ingredient: no conversion, use as-is
				key := strings.ToLower(ing.name) + "|" + ing.unit
				if totals[key] == nil {
					totals[key] = &totalNeed{
						name:    ing.name,
						unit:    ing.unit,
						recipes: map[string]bool{},
					}
				}
				totals[key].total += rawNeeded
				markRecipe(totals[key].recipes, e.recipeName)
			}
		}
	}

	// Subtract current inventory from totals to get shortfalls
	needs := map[string]*ShoppingNeed{}
	for key, t := range totals {
		var shortfall float64
		if t.invID != nil && !t.incompatible {
			available := simulated[*t.invID]
			shortfall = t.total - available
		} else {
			shortfall = t.total
		}
		if shortfall > 0 {
			needs[key] = &ShoppingNeed{
				ProductID:      t.invID,
				Name:           t.name,
				Unit:           t.unit,
				QuantityNeeded: shortfall,
				Recipes:        sortedKeys(t.recipes),
			}
		}
	}

	var result []ShoppingNeed
	for _, n := range needs {
		result = append(result, *n)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Unit < result[j].Unit
	})
	return result, nil
}

func markRecipe(m map[string]bool, name string) {
	if name != "" {
		m[name] = true
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
