package handlers

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"kitchen_manager/services"
)

func RegisterCalendar(mux *http.ServeMux, db *sql.DB, hub ...*Hub) {
	var wsHub *Hub
	if len(hub) > 0 {
		wsHub = hub[0]
	}
	broadcastCalendar := func() {
		if wsHub != nil {
			wsHub.Broadcast("calendar_updated")
		}
	}

	mux.HandleFunc("POST /api/calendar/", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Date     string `json:"date"`
			MealSlot string `json:"meal_slot"`
			RecipeID int64  `json:"recipe_id"`
			Servings int    `json:"servings"`
		}
		if err := ReadJSON(r, &input); err != nil || input.RecipeID == 0 {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if input.MealSlot == "" {
			input.MealSlot = "dinner"
		}
		// Validate date format
		if _, err := time.Parse("2006-01-02", input.Date); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid date format, use YYYY-MM-DD")
			return
		}
		var recipeServings int
		if err := db.QueryRow(`SELECT servings FROM recipes WHERE id=?`, input.RecipeID).Scan(&recipeServings); err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "recipe not found")
			return
		} else if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if input.Servings <= 0 {
			input.Servings = recipeServings
			if input.Servings <= 0 {
				input.Servings = 1
			}
		}
		res, err := db.Exec(`INSERT INTO meal_calendar (date,meal_slot,recipe_id,servings) VALUES (?,?,?,?)`,
			input.Date, input.MealSlot, input.RecipeID, input.Servings)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		WriteJSON(w, http.StatusCreated, map[string]any{
			"id": id, "date": input.Date, "meal_slot": input.MealSlot,
			"recipe_id": input.RecipeID, "servings": input.Servings,
		})
		broadcastCalendar()
	})

	mux.HandleFunc("GET /api/calendar/week", func(w http.ResponseWriter, r *http.Request) {
		startStr := r.URL.Query().Get("start")
		if startStr == "" {
			startStr = time.Now().Format("2006-01-02")
		}
		t, err := time.Parse("2006-01-02", startStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid start date")
			return
		}
		endStr := t.AddDate(0, 0, 6).Format("2006-01-02")
		rows, err := db.Query(`SELECT id,date,meal_slot,recipe_id,servings FROM meal_calendar WHERE date>=? AND date<=? ORDER BY date`, startStr, endStr)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		var entries []map[string]any
		for rows.Next() {
			var id, recipeID int64
			var date, mealSlot string
			var servings int
			if err := rows.Scan(&id, &date, &mealSlot, &recipeID, &servings); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			entries = append(entries, map[string]any{
				"id": id, "date": date, "meal_slot": mealSlot,
				"recipe_id": recipeID, "servings": servings,
			})
		}
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if entries == nil {
			entries = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, entries)
	})

	mux.HandleFunc("DELETE /api/calendar/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		res, err := db.Exec(`DELETE FROM meal_calendar WHERE id=?`, id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		broadcastCalendar()
	})

	mux.HandleFunc("POST /api/calendar/generate-weekly-shopping", func(w http.ResponseWriter, r *http.Request) {
		startStr := r.URL.Query().Get("start")
		if startStr == "" {
			// Try to read week_start from JSON body
			var body struct {
				WeekStart string `json:"week_start"`
			}
			ReadJSON(r, &body) //nolint:errcheck — body is optional
			startStr = body.WeekStart
		}
		if startStr == "" {
			startStr = time.Now().Format("2006-01-02")
		}
		needs, err := services.GenerateWeeklyShopping(db, startStr)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback() //nolint:errcheck
		items := []map[string]any{}
		created, merged := 0, 0
		for _, n := range needs {
			mr, err := services.MergeIntoShoppingList(tx, services.ListAddition{
				InventoryID: n.InventoryID, Name: n.Name, Unit: n.Unit, Quantity: n.QuantityNeeded,
				Source: "calendar", Ref: startStr,
				Label: "Week of " + startStr + ": " + strings.Join(n.Recipes, ", "),
				Mode:  services.MergeReplace,
			})
			if err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if mr.Created {
				created++
			} else {
				merged++
			}
			items = append(items, map[string]any{
				"id": mr.ShoppingID, "inventory_id": n.InventoryID, "name": n.Name,
				"quantity_needed": n.QuantityNeeded, "unit": n.Unit,
				"checked": false, "source": "calendar", "merged": !mr.Created,
				"total_quantity": mr.Quantity, "total_unit": mr.Unit,
			})
		}
		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"week_start": startStr, "items": items, "added": created, "merged": merged})
		if len(items) > 0 {
			if wsHub != nil {
				wsHub.Broadcast("shopping_updated")
			}
		}
		broadcastCalendar()
	})
}
