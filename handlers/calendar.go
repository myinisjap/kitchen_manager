package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"kitchen_manager/services"
)

func RegisterCalendar(mux *http.ServeMux, db *sql.DB) {
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
		if input.Servings == 0 {
			input.Servings = 1
		}
		// Validate date format
		if _, err := time.Parse("2006-01-02", input.Date); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid date format, use YYYY-MM-DD")
			return
		}
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM recipes WHERE id=?`, input.RecipeID).Scan(&exists); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if exists == 0 {
			WriteError(w, http.StatusNotFound, "recipe not found")
			return
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
	})

	mux.HandleFunc("POST /api/calendar/generate-weekly-shopping", func(w http.ResponseWriter, r *http.Request) {
		startStr := r.URL.Query().Get("start")
		if startStr == "" {
			startStr = time.Now().Format("2006-01-02")
		}
		needs, err := services.GenerateWeeklyShopping(db, startStr)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var items []map[string]any
		for _, n := range needs {
			res, err := db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,'calendar')`,
				n.InventoryID, n.Name, n.QuantityNeeded, n.Unit)
			if err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			newID, _ := res.LastInsertId()
			items = append(items, map[string]any{
				"id": newID, "inventory_id": n.InventoryID, "name": n.Name,
				"quantity_needed": n.QuantityNeeded, "unit": n.Unit,
				"checked": false, "source": "calendar",
			})
		}
		if items == nil {
			items = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"week_start": startStr, "items": items})
	})
}
