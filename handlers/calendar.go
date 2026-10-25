package handlers

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"kitchen_manager/services"
)

var mealSlots = map[string]bool{"breakfast": true, "lunch": true, "dinner": true, "snack": true}

func normalizeMealSlot(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	return s, mealSlots[s]
}

const calendarEntrySelect = `SELECT mc.id, mc.date, mc.meal_slot, mc.recipe_id, mc.servings, mh.id
	FROM meal_calendar mc LEFT JOIN meal_history mh ON mh.calendar_entry_id = mc.id`

type rowScanner interface{ Scan(...any) error }

func scanCalendarEntry(s rowScanner) (map[string]any, error) {
	var id, recipeID int64
	var date, mealSlot string
	var servings int
	var mh sql.NullInt64
	if err := s.Scan(&id, &date, &mealSlot, &recipeID, &servings, &mh); err != nil {
		return nil, err
	}
	var mhID any
	if mh.Valid {
		mhID = mh.Int64
	}
	return map[string]any{
		"id": id, "date": date, "meal_slot": mealSlot,
		"recipe_id": recipeID, "servings": servings,
		"cooked": mh.Valid, "meal_history_id": mhID,
	}, nil
}

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

	mux.HandleFunc("PATCH /api/calendar/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var input struct {
			Date     *string `json:"date"`
			MealSlot *string `json:"meal_slot"`
			Servings *int    `json:"servings"`
		}
		if err := ReadJSON(r, &input); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if input.Date == nil && input.MealSlot == nil && input.Servings == nil {
			WriteError(w, http.StatusBadRequest, "no fields to update")
			return
		}
		sets := []string{}
		args := []any{}
		if input.Date != nil {
			if _, err := time.Parse("2006-01-02", *input.Date); err != nil {
				WriteError(w, http.StatusBadRequest, "invalid date format, use YYYY-MM-DD")
				return
			}
			sets = append(sets, "date=?")
			args = append(args, *input.Date)
		}
		if input.MealSlot != nil {
			s, ok := normalizeMealSlot(*input.MealSlot)
			if !ok {
				WriteError(w, http.StatusBadRequest, "invalid meal_slot (breakfast, lunch, dinner, snack)")
				return
			}
			sets = append(sets, "meal_slot=?")
			args = append(args, s)
		}
		if input.Servings != nil {
			if *input.Servings <= 0 {
				WriteError(w, http.StatusBadRequest, "servings must be positive")
				return
			}
			sets = append(sets, "servings=?")
			args = append(args, *input.Servings)
		}
		args = append(args, id)
		res, err := db.Exec(`UPDATE meal_calendar SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		e, err := scanCalendarEntry(db.QueryRow(calendarEntrySelect+` WHERE mc.id=?`, id))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, e)
		broadcastCalendar()
	})

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
		if strings.TrimSpace(input.MealSlot) == "" {
			input.MealSlot = "dinner"
		} else if s, ok := normalizeMealSlot(input.MealSlot); ok {
			input.MealSlot = s
		} else {
			WriteError(w, http.StatusBadRequest, "invalid meal_slot (breakfast, lunch, dinner, snack)")
			return
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
			"cooked": false, "meal_history_id": nil,
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
		rows, err := db.Query(calendarEntrySelect+` WHERE mc.date>=? AND mc.date<=? ORDER BY mc.date, mc.id`, startStr, endStr)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		var entries []map[string]any
		for rows.Next() {
			e, err := scanCalendarEntry(rows)
			if err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			entries = append(entries, e)
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
		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := tx.Exec(`UPDATE meal_history SET calendar_entry_id=NULL WHERE calendar_entry_id=?`, id); err != nil {
			tx.Rollback()
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		res, err := tx.Exec(`DELETE FROM meal_calendar WHERE id=?`, id)
		if err != nil {
			tx.Rollback()
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			tx.Rollback()
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
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
