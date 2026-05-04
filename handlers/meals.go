package handlers

import (
	"database/sql"
	"math"
	"net/http"
	"strconv"
	"time"

	"kitchen_manager/internal/auth"
)

func RegisterMeals(mux *http.ServeMux, db *sql.DB) {
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
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if recipeServings == 0 {
			recipeServings = 1
		}
		if input.ServingsMade <= 0 {
			input.ServingsMade = recipeServings
		}

		cookedAt := time.Now().UTC().Format(time.RFC3339)
		if input.CookedAt != "" {
			if _, err := time.Parse(time.RFC3339, input.CookedAt); err == nil {
				cookedAt = input.CookedAt
			}
		}
		if len(input.Notes) > 500 {
			input.Notes = input.Notes[:500]
		}

		// Load ingredients
		ingRows, err := db.Query(`SELECT id, inventory_id, name, quantity, unit FROM recipe_ingredients WHERE recipe_id=?`, input.RecipeID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
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
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			ingredients = append(ingredients, ing)
		}
		ingRows.Close()

		// Begin transaction
		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
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

		for _, ing := range ingredients {
			scale := float64(input.ServingsMade) / float64(recipeServings)
			qtyUsed := ing.Quantity * scale

			var costCents *int64
			var invIDPtr *int64

			if ing.InventoryID.Valid {
				invID := ing.InventoryID.Int64
				invIDPtr = &invID

				// Fetch current inventory quantity and cost
				var currentQty float64
				var unitCostCents sql.NullInt64
				err := tx.QueryRow(`SELECT quantity, unit_cost_cents FROM inventory WHERE id=?`, invID).Scan(&currentQty, &unitCostCents)
				if err == nil {
					newQty := currentQty - qtyUsed
					if newQty < 0 {
						newQty = 0
					}
					if _, err := tx.Exec(`UPDATE inventory SET quantity=? WHERE id=?`, newQty, invID); err != nil {
						tx.Rollback()
						WriteError(w, http.StatusInternalServerError, err.Error())
						return
					}
					// Log inventory history
					recID := input.RecipeID
					_ = LogHistory(db, tx, LogHistoryParams{
						InventoryID:    invID,
						ItemName:       ing.Name,
						ChangeType:     "deduct",
						QuantityBefore: &currentQty,
						QuantityAfter:  &newQty,
						Unit:           ing.Unit,
						Source:         "meal_cooked",
						RecipeID:       &recID,
						ChangedBy:      auth.EmailFromContext(r.Context()),
					})
					if unitCostCents.Valid && unitCostCents.Int64 > 0 {
						c := int64(math.Round(qtyUsed * float64(unitCostCents.Int64)))
						costCents = &c
						totalCost += c
						hasCost = true
					}
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
			WriteError(w, http.StatusInternalServerError, err.Error())
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
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		WriteJSON(w, http.StatusCreated, map[string]any{
			"id":               mealID,
			"recipe_id":        input.RecipeID,
			"recipe_name":      recipeName,
			"cooked_at":        cookedAt,
			"servings_made":    input.ServingsMade,
			"total_cost_cents": totalCostPtr,
			"notes":            input.Notes,
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
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		rows, err := db.Query(listQ, append(args, limit, offset)...)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		var meals []map[string]any
		var mealIDs []int64
		for rows.Next() {
			var id, recipeID int64
			var recipeName, cookedAt, notes string
			var servingsMade int
			var totalCost sql.NullInt64
			if err := rows.Scan(&id, &recipeID, &recipeName, &cookedAt, &servingsMade, &totalCost, &notes); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			m := map[string]any{
				"id":            id,
				"recipe_id":     recipeID,
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
			WriteError(w, http.StatusInternalServerError, err.Error())
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
		tzOffsetStr := r.URL.Query().Get("tz_offset")
		tzOffsetMins := 0
		if tzOffsetStr != "" {
			if v, err := strconv.Atoi(tzOffsetStr); err == nil {
				tzOffsetMins = v
			}
		}

		now := time.Now().UTC().Add(time.Duration(tzOffsetMins) * time.Minute)
		// Week boundary: Monday 00:00 local
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		weekStart := now.AddDate(0, 0, -(weekday - 1))
		weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, weekStart.Location())
		// Month boundary: 1st of current month
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

		weekStartStr := weekStart.UTC().Format("2006-01-02T15:04:05")
		monthStartStr := monthStart.UTC().Format("2006-01-02T15:04:05")

		var mealsThisWeek, mealsThisMonth int
		var spendWeek, spendMonth sql.NullInt64
		_ = db.QueryRow(`SELECT COUNT(*), SUM(total_cost_cents) FROM meal_history WHERE cooked_at >= ?`, weekStartStr).Scan(&mealsThisWeek, &spendWeek)
		_ = db.QueryRow(`SELECT COUNT(*), SUM(total_cost_cents) FROM meal_history WHERE cooked_at >= ?`, monthStartStr).Scan(&mealsThisMonth, &spendMonth)

		// Most cooked (top 5 all-time)
		mcRows, err := db.Query(`
			SELECT recipe_id, recipe_name, COUNT(*) as cnt
			FROM meal_history
			GROUP BY recipe_id, recipe_name
			ORDER BY cnt DESC
			LIMIT 5`)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer mcRows.Close()
		var mostCooked []map[string]any
		for mcRows.Next() {
			var rid int64
			var rname string
			var cnt int
			if err := mcRows.Scan(&rid, &rname, &cnt); err != nil {
				continue
			}
			mostCooked = append(mostCooked, map[string]any{
				"recipe_id":   rid,
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
