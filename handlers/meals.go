package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"kitchen_manager/units"
	"math"
	"net/http"
	"strconv"
	"time"
	_ "time/tzdata"

	"kitchen_manager/internal/auth"
)

func RegisterMeals(mux *http.ServeMux, db *sql.DB, hub ...*Hub) {
	var wsHub *Hub
	if len(hub) > 0 {
		wsHub = hub[0]
	}
	// POST /api/meals — log a cooked meal
	mux.HandleFunc("POST /api/meals", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			RecipeID     int64  `json:"recipe_id"`
			ServingsMade int    `json:"servings_made"`
			CookedAt     string `json:"cooked_at"`
			Notes        string `json:"notes"`
		}
		if err := ReadJSON(r, &input); err != nil || input.RecipeID == 0 {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}

		// Validate recipe exists
		var recipeName string
		var recipeServings int
		err := db.QueryRow(`SELECT name, servings FROM recipes WHERE id=?`, input.RecipeID).Scan(&recipeName, &recipeServings)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "recipe not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if recipeServings == 0 {
			recipeServings = 1
		}
		if input.ServingsMade <= 0 {
			input.ServingsMade = recipeServings
		}

		cookedAt := mealsNow().UTC().Format(time.RFC3339)
		if input.CookedAt != "" {
			if t, err := time.Parse(time.RFC3339, input.CookedAt); err == nil {
				cookedAt = t.UTC().Format(time.RFC3339)
			}
		}
		if len(input.Notes) > 500 {
			input.Notes = input.Notes[:500]
		}

		// Load ingredients
		ingRows, err := db.Query(`SELECT id, inventory_id, name, quantity, unit FROM recipe_ingredients WHERE recipe_id=?`, input.RecipeID)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		type ingredient struct {
			ID          int64
			InventoryID sql.NullInt64
			Name        string
			Quantity    float64
			Unit        string
		}
		var ingredients []ingredient
		for ingRows.Next() {
			var ing ingredient
			if err := ingRows.Scan(&ing.ID, &ing.InventoryID, &ing.Name, &ing.Quantity, &ing.Unit); err != nil {
				ingRows.Close()
				WriteServerError(w, r, err)
				return
			}
			ingredients = append(ingredients, ing)
		}
		ingRows.Close()

		// Begin transaction
		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}

		var totalCost int64
		hasCost := false
		type mealIngRow struct {
			inventoryID *int64
			name        string
			qtyUsed     float64
			unit        string
			costCents   *int64
		}
		var mealIngs []mealIngRow
		remap := map[int64]int64{}
		warnings := []string{}

		for _, ing := range ingredients {
			scale := units.ServingsScale(input.ServingsMade, recipeServings)
			qtyUsed := ing.Quantity * scale

			var costCents *int64
			var invIDPtr *int64

			if ing.InventoryID.Valid {
				invID := ing.InventoryID.Int64
				if m, ok := remap[invID]; ok {
					invID = m
				}
				recID := input.RecipeID
				dres, derr := deductStock(db, tx, deductRequest{
					ItemID:    invID,
					Qty:       qtyUsed,
					Unit:      ing.Unit,
					Source:    "meal_cooked",
					RecipeID:  &recID,
					ChangedBy: auth.EmailFromContext(r.Context()),
				})
				switch {
				case derr == nil:
					// Reference the row that now holds this item's stock; the
					// original row may have been folded into a sibling. The
					// recorded quantity/unit stay in the recipe's unit.
					if dres.ResultItemID != 0 {
						rid := dres.ResultItemID
						invIDPtr = &rid
						remap[ing.InventoryID.Int64] = rid
					}
					if dres.UnitCostCents > 0 {
						c := int64(math.Round(dres.Requested * float64(dres.UnitCostCents)))
						costCents = &c
						totalCost += c
						hasCost = true
					}
				case errors.Is(derr, errItemNotFound):
					// Stale link: treat as an unlinked ingredient.
				case errors.Is(derr, errIncompatibleUnits):
					// Skip deduction and cost for this ingredient only; keep the link.
					lid := invID
					invIDPtr = &lid
					warnings = append(warnings, fmt.Sprintf("Couldn't deduct %s %s of %s: stock is tracked in %s", strconv.FormatFloat(qtyUsed, 'f', -1, 64), ing.Unit, ing.Name, dres.Unit))
				default:
					tx.Rollback()
					WriteServerError(w, r, derr)
					return
				}
			}

			mealIngs = append(mealIngs, mealIngRow{
				inventoryID: invIDPtr,
				name:        ing.Name,
				qtyUsed:     qtyUsed,
				unit:        ing.Unit,
				costCents:   costCents,
			})
		}

		var totalCostPtr *int64
		if hasCost {
			totalCostPtr = &totalCost
		}

		// Insert meal_history row
		res, err := tx.Exec(
			`INSERT INTO meal_history (recipe_id, recipe_name, cooked_at, servings_made, total_cost_cents, notes) VALUES (?,?,?,?,?,?)`,
			input.RecipeID, recipeName, cookedAt, input.ServingsMade, totalCostPtr, input.Notes,
		)
		if err != nil {
			tx.Rollback()
			WriteServerError(w, r, err)
			return
		}
		mealID, _ := res.LastInsertId()

		// Insert meal_history_ingredients
		for _, mi := range mealIngs {
			if _, err := tx.Exec(
				`INSERT INTO meal_history_ingredients (meal_history_id, inventory_id, ingredient_name, quantity_used, unit, cost_cents) VALUES (?,?,?,?,?,?)`,
				mealID, mi.inventoryID, mi.name, mi.qtyUsed, mi.unit, mi.costCents,
			); err != nil {
				tx.Rollback()
				WriteServerError(w, r, err)
				return
			}
		}

		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}

		if wsHub != nil {
			wsHub.Broadcast("inventory_updated")
		}

		WriteJSON(w, http.StatusCreated, map[string]any{
			"id":               mealID,
			"recipe_id":        input.RecipeID,
			"recipe_name":      recipeName,
			"cooked_at":        cookedAt,
			"servings_made":    input.ServingsMade,
			"total_cost_cents": totalCostPtr,
			"notes":            input.Notes,
			"warnings":         warnings,
		})
	})

	// GET /api/meals — paginated meal history list
	mux.HandleFunc("GET /api/meals", func(w http.ResponseWriter, r *http.Request) {
		limitStr := r.URL.Query().Get("limit")
		offsetStr := r.URL.Query().Get("offset")
		recipeIDStr := r.URL.Query().Get("recipe_id")
		limit := 20
		offset := 0
		if limitStr != "" {
			if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 100 {
				limit = v
			}
		}
		if offsetStr != "" {
			if v, err := strconv.Atoi(offsetStr); err == nil && v >= 0 {
				offset = v
			}
		}

		countQ := `SELECT COUNT(*) FROM meal_history`
		listQ := `SELECT id, recipe_id, recipe_name, cooked_at, servings_made, total_cost_cents, notes FROM meal_history`
		args := []any{}
		if recipeIDStr != "" {
			if rid, err := strconv.ParseInt(recipeIDStr, 10, 64); err == nil {
				countQ += ` WHERE recipe_id=?`
				listQ += ` WHERE recipe_id=?`
				args = append(args, rid)
			}
		}
		listQ += ` ORDER BY cooked_at DESC LIMIT ? OFFSET ?`

		var total int
		if err := db.QueryRow(countQ, args...).Scan(&total); err != nil {
			WriteServerError(w, r, err)
			return
		}

		rows, err := db.Query(listQ, append(args, limit, offset)...)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer rows.Close()

		var meals []map[string]any
		var mealIDs []int64
		for rows.Next() {
			var id int64
			var recipeID sql.NullInt64
			var recipeName, cookedAt, notes string
			var servingsMade int
			var totalCost sql.NullInt64
			if err := rows.Scan(&id, &recipeID, &recipeName, &cookedAt, &servingsMade, &totalCost, &notes); err != nil {
				WriteServerError(w, r, err)
				return
			}
			var recipeIDVal any
			if recipeID.Valid {
				recipeIDVal = recipeID.Int64
			}
			m := map[string]any{
				"id":            id,
				"recipe_id":     recipeIDVal,
				"recipe_name":   recipeName,
				"cooked_at":     cookedAt,
				"servings_made": servingsMade,
				"notes":         notes,
				"ingredients":   []map[string]any{},
			}
			if totalCost.Valid {
				m["total_cost_cents"] = totalCost.Int64
			} else {
				m["total_cost_cents"] = nil
			}
			meals = append(meals, m)
			mealIDs = append(mealIDs, id)
		}
		if err := rows.Err(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if meals == nil {
			meals = []map[string]any{}
		}

		// Load ingredients for each meal
		for i, mealID := range mealIDs {
			ingRows, err := db.Query(
				`SELECT id, meal_history_id, inventory_id, ingredient_name, quantity_used, unit, cost_cents FROM meal_history_ingredients WHERE meal_history_id=?`,
				mealID,
			)
			if err != nil {
				continue
			}
			var ings []map[string]any
			for ingRows.Next() {
				var iid, mhid int64
				var invID sql.NullInt64
				var ingName, unit string
				var qtyUsed float64
				var costCents sql.NullInt64
				if err := ingRows.Scan(&iid, &mhid, &invID, &ingName, &qtyUsed, &unit, &costCents); err != nil {
					continue
				}
				ing := map[string]any{
					"id":              iid,
					"meal_history_id": mhid,
					"ingredient_name": ingName,
					"quantity_used":   qtyUsed,
					"unit":            unit,
				}
				if invID.Valid {
					ing["inventory_id"] = invID.Int64
				} else {
					ing["inventory_id"] = nil
				}
				if costCents.Valid {
					ing["cost_cents"] = costCents.Int64
				} else {
					ing["cost_cents"] = nil
				}
				ings = append(ings, ing)
			}
			ingRows.Close()
			if ings != nil {
				meals[i]["ingredients"] = ings
			}
		}

		WriteJSON(w, http.StatusOK, map[string]any{"total": total, "meals": meals})
	})

	// GET /api/meals/stats — aggregated statistics
	mux.HandleFunc("GET /api/meals/stats", func(w http.ResponseWriter, r *http.Request) {
		var loc *time.Location = time.UTC
		if name := r.URL.Query().Get("tz"); name != "" {
			if l, err := time.LoadLocation(name); err == nil {
				loc = l
			}
		}
		if loc == time.UTC {
			if v, err := strconv.Atoi(r.URL.Query().Get("tz_offset")); err == nil && v >= -840 && v <= 840 {
				loc = time.FixedZone("", v*60)
			}
		}

		weekStart, monthStart := mealStatsBounds(mealsNow(), loc)
		weekStartStr := weekStart.Format(time.RFC3339)
		monthStartStr := monthStart.Format(time.RFC3339)

		var mealsThisWeek, mealsThisMonth int
		var spendWeek, spendMonth sql.NullInt64
		if err := db.QueryRow(`SELECT COUNT(*), SUM(total_cost_cents) FROM meal_history WHERE cooked_at >= ?`, weekStartStr).Scan(&mealsThisWeek, &spendWeek); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if err := db.QueryRow(`SELECT COUNT(*), SUM(total_cost_cents) FROM meal_history WHERE cooked_at >= ?`, monthStartStr).Scan(&mealsThisMonth, &spendMonth); err != nil {
			WriteServerError(w, r, err)
			return
		}

		// Most cooked (top 5 all-time)
		mcRows, err := db.Query(`
			SELECT recipe_id, recipe_name, COUNT(*) as cnt
			FROM meal_history
			GROUP BY recipe_id, recipe_name
			ORDER BY cnt DESC
			LIMIT 5`)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer mcRows.Close()
		var mostCooked []map[string]any
		for mcRows.Next() {
			var rid sql.NullInt64
			var rname string
			var cnt int
			if err := mcRows.Scan(&rid, &rname, &cnt); err != nil {
				continue
			}
			var ridVal any
			if rid.Valid {
				ridVal = rid.Int64
			}
			mostCooked = append(mostCooked, map[string]any{
				"recipe_id":   ridVal,
				"recipe_name": rname,
				"count":       cnt,
			})
		}
		if mostCooked == nil {
			mostCooked = []map[string]any{}
		}

		resp := map[string]any{
			"total_meals_this_week":        mealsThisWeek,
			"total_meals_this_month":       mealsThisMonth,
			"total_spend_this_week_cents":  nil,
			"total_spend_this_month_cents": nil,
			"most_cooked":                  mostCooked,
		}
		if spendWeek.Valid {
			resp["total_spend_this_week_cents"] = spendWeek.Int64
		}
		if spendMonth.Valid {
			resp["total_spend_this_month_cents"] = spendMonth.Int64
		}
		WriteJSON(w, http.StatusOK, resp)
	})
}

// mealsNow is the clock used by meal handlers (overridable in tests).
var mealsNow = time.Now

// mealStatsBounds returns the start of the current ISO week (Monday 00:00) and
// current month (1st 00:00) in loc, converted to UTC.
func mealStatsBounds(now time.Time, loc *time.Location) (weekStart, monthStart time.Time) {
	local := now.In(loc)
	wd := int(local.Weekday())
	if wd == 0 {
		wd = 7
	}
	ws := time.Date(local.Year(), local.Month(), local.Day()-(wd-1), 0, 0, 0, 0, loc)
	ms := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	return ws.UTC(), ms.UTC()
}
