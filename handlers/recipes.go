package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kitchen_manager/services"
	"kitchen_manager/units"
)

func RegisterRecipes(mux *http.ServeMux, db *sql.DB, hub ...*Hub) {
	var wsHub *Hub
	if len(hub) > 0 {
		wsHub = hub[0]
	}
	broadcastRecipes := func() {
		if wsHub != nil {
			wsHub.Broadcast("recipes_updated")
		}
	}

	mux.HandleFunc("POST /api/recipes/", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			Instructions string `json:"instructions"`
			Tags         string `json:"tags"`
			Servings     int    `json:"servings"`
			Ingredients  []struct {
				ProductID   *int64  `json:"product_id"`
				InventoryID *int64  `json:"inventory_id"` // legacy: a stock lot id
				Name        string  `json:"name"`
				Quantity    float64 `json:"quantity"`
				Unit        string  `json:"unit"`
			} `json:"ingredients"`
		}
		if err := ReadJSON(r, &input); err != nil || input.Name == "" {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if input.Servings == 0 {
			input.Servings = 1
		}
		for _, ing := range input.Ingredients {
			if ing.Unit != "" && !units.IsValid(units.Unit(ing.Unit)) {
				WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid unit %q for ingredient %q; valid units: %s", ing.Unit, ing.Name, units.ValidList()))
				return
			}
		}
		res, err := db.Exec(`INSERT INTO recipes (name,description,instructions,tags,servings) VALUES (?,?,?,?,?)`,
			input.Name, input.Description, input.Instructions, input.Tags, input.Servings)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		recipeID, _ := res.LastInsertId()
		for _, ing := range input.Ingredients {
			ingName := strings.TrimSpace(ing.Name)
			prodID := ingredientProductID(db, ing.ProductID, ing.InventoryID, ingName)
			if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id,product_id,name,quantity,unit) VALUES (?,?,?,?,?)`,
				recipeID, prodID, ingName, ing.Quantity, ing.Unit); err != nil {
				WriteServerError(w, r, err)
				return
			}
		}
		recipe, err := getRecipeWithIngredients(db, recipeID)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, recipe)
		broadcastRecipes()
	})

	mux.HandleFunc("GET /api/recipes/", func(w http.ResponseWriter, r *http.Request) {
		tag := r.URL.Query().Get("tag")
		availableOnly := r.URL.Query().Get("available_only") == "true"

		q := `SELECT id,name,description,instructions,tags,servings FROM recipes`
		if r.URL.Query().Get("include_archived") != "true" {
			q += ` WHERE archived=0`
		}
		rows, err := db.Query(q)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}

		type recipeRow struct {
			id   int64
			tags string
		}
		var candidates []recipeRow
		for rows.Next() {
			var id int64
			var name, desc, instructions, tags string
			var servings int
			if err := rows.Scan(&id, &name, &desc, &instructions, &tags, &servings); err != nil {
				rows.Close()
				WriteServerError(w, r, err)
				return
			}
			if tag != "" {
				matched := false
				for _, t := range strings.Split(tags, ",") {
					if strings.TrimSpace(t) == tag {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			candidates = append(candidates, recipeRow{id: id, tags: tags})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			WriteServerError(w, r, err)
			return
		}
		rows.Close()

		var result []map[string]any
		for _, c := range candidates {
			recipe, err := getRecipeWithIngredients(db, c.id)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			avail, err := recipeIsAvailable(db, recipe)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			if availableOnly && !avail.Available {
				continue
			}
			incompatible := avail.Incompatible
			if incompatible == nil {
				incompatible = []string{}
			}
			recipe["incompatible_ingredients"] = incompatible
			result = append(result, recipe)
		}
		if result == nil {
			result = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, result)
	})

	mux.HandleFunc("GET /api/recipes/use-it-up", func(w http.ResponseWriter, r *http.Request) {
		days, cutoff := expiryDaysAndCutoff(r, db)
		limit := 5
		if s := r.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 50 {
			limit = 50
		}
		recipes, err := services.RankUseItUp(db, cutoff, limit)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"days": days, "cutoff": cutoff, "recipes": recipes})
	})

	mux.HandleFunc("GET /api/recipes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		recipe, err := getRecipeWithIngredients(db, id)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, recipe)
	})

	mux.HandleFunc("GET /api/recipes/{id}/future-calendar", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		today := time.Now().Format("2006-01-02")
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM meal_calendar WHERE recipe_id=? AND date>=?`, id, today).Scan(&count); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"count": count})
	})

	setArchived := func(val int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, ok := pathIDFromPattern(r, "id")
			if !ok {
				WriteError(w, http.StatusBadRequest, "invalid id")
				return
			}
			res, err := db.Exec(`UPDATE recipes SET archived=? WHERE id=?`, val, id)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			if n, _ := res.RowsAffected(); n == 0 {
				WriteError(w, http.StatusNotFound, "not found")
				return
			}
			recipe, err := getRecipeWithIngredients(db, id)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			WriteJSON(w, http.StatusOK, recipe)
			broadcastRecipes()
		}
	}
	mux.HandleFunc("POST /api/recipes/{id}/archive", setArchived(1))
	mux.HandleFunc("POST /api/recipes/{id}/unarchive", setArchived(0))

	mux.HandleFunc("DELETE /api/recipes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM recipes WHERE id=?`, id).Scan(&exists); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if exists == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		// Keep cooking history (meal_history stores recipe_name): just unlink the recipe.
		if _, err := tx.Exec(`UPDATE meal_history SET recipe_id=NULL WHERE recipe_id=?`, id); err != nil {
			tx.Rollback()
			WriteServerError(w, r, err)
			return
		}
		if _, err := tx.Exec(`UPDATE inventory_history SET recipe_id=NULL WHERE recipe_id=?`, id); err != nil {
			tx.Rollback()
			WriteServerError(w, r, err)
			return
		}
		if _, err := tx.Exec(`UPDATE meal_history SET calendar_entry_id=NULL WHERE calendar_entry_id IN (SELECT id FROM meal_calendar WHERE recipe_id=?)`, id); err != nil {
			tx.Rollback()
			WriteServerError(w, r, err)
			return
		}
		if _, err := tx.Exec(`DELETE FROM meal_calendar WHERE recipe_id=?`, id); err != nil {
			tx.Rollback()
			WriteServerError(w, r, err)
			return
		}
		if _, err := tx.Exec(`DELETE FROM recipe_ingredients WHERE recipe_id=?`, id); err != nil {
			tx.Rollback()
			WriteServerError(w, r, err)
			return
		}
		if _, err := tx.Exec(`DELETE FROM recipes WHERE id=?`, id); err != nil {
			tx.Rollback()
			WriteServerError(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		broadcastRecipes()
	})

	mux.HandleFunc("PUT /api/recipes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var input struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			Instructions string `json:"instructions"`
			Tags         string `json:"tags"`
			Servings     int    `json:"servings"`
			Ingredients  []struct {
				ProductID   *int64  `json:"product_id"`
				InventoryID *int64  `json:"inventory_id"` // legacy: a stock lot id
				Name        string  `json:"name"`
				Quantity    float64 `json:"quantity"`
				Unit        string  `json:"unit"`
			} `json:"ingredients"`
		}
		if err := ReadJSON(r, &input); err != nil || input.Name == "" {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if input.Servings == 0 {
			input.Servings = 1
		}
		for _, ing := range input.Ingredients {
			if ing.Unit != "" && !units.IsValid(units.Unit(ing.Unit)) {
				WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid unit %q for ingredient %q", ing.Unit, ing.Name))
				return
			}
		}
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM recipes WHERE id=?`, id).Scan(&exists); err != nil {
			WriteServerError(w, r, err)
			return
		}
		if exists == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		if _, err := tx.Exec(`UPDATE recipes SET name=?,description=?,instructions=?,tags=?,servings=? WHERE id=?`,
			input.Name, input.Description, input.Instructions, input.Tags, input.Servings, id); err != nil {
			tx.Rollback()
			WriteServerError(w, r, err)
			return
		}
		if _, err := tx.Exec(`DELETE FROM recipe_ingredients WHERE recipe_id=?`, id); err != nil {
			tx.Rollback()
			WriteServerError(w, r, err)
			return
		}
		for _, ing := range input.Ingredients {
			ingName := strings.TrimSpace(ing.Name)
			prodID := ingredientProductID(tx, ing.ProductID, ing.InventoryID, ingName)
			if _, err := tx.Exec(`INSERT INTO recipe_ingredients (recipe_id,product_id,name,quantity,unit) VALUES (?,?,?,?,?)`,
				id, prodID, ingName, ing.Quantity, ing.Unit); err != nil {
				tx.Rollback()
				WriteServerError(w, r, err)
				return
			}
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		recipe, err := getRecipeWithIngredients(db, id)
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, recipe)
		broadcastRecipes()
	})

	mux.HandleFunc("POST /api/recipes/{id}/add-to-shopping-list", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		servingsParam := r.URL.Query().Get("servings")
		requestedServings := 0
		if servingsParam != "" {
			var s int
			if _, err := fmt.Sscanf(servingsParam, "%d", &s); err == nil && s > 0 {
				requestedServings = s
			}
		}
		recipe, err := getRecipeWithIngredients(db, id)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		recipeServings := recipe["servings"].(int)
		if recipeServings == 0 {
			recipeServings = 1
		}
		if requestedServings <= 0 {
			requestedServings = recipeServings
		}
		scale := units.ServingsScale(requestedServings, recipeServings)
		ings := recipe["ingredients"].([]map[string]any)
		var additions []services.ListAddition
		for _, ing := range ings {
			needed := ing["quantity"].(float64) * scale
			ingUnit := units.Unit(ing["unit"].(string))
			have := 0.0
			displayUnit := ingUnit

			if pid, ok := ing["product_id"].(int64); ok {
				total, targetUnit, err := productStock(db, pid)
				if err == nil {
					converted, convErr := units.Convert(needed, ingUnit, units.Unit(targetUnit))
					if convErr == nil {
						needed = converted
						have = total
						displayUnit = units.Unit(targetUnit)
					}
					// else: units can't be compared; add the full need in the
					// recipe's unit and don't subtract mismatched stock.
				}
			}

			shortfall := needed - have
			if shortfall > 0 {
				var invPtr *int64
				if v, ok := ing["product_id"].(int64); ok {
					invPtr = &v
				}
				additions = append(additions, services.ListAddition{
					ProductID: invPtr, Name: ing["name"].(string), Unit: string(displayUnit),
					Quantity: shortfall, Source: "recipe", Ref: strconv.FormatInt(id, 10),
					Label: recipe["name"].(string), Mode: services.MergeAdd,
				})
			}
		}
		// A second "add recipe" is additive (the user asked again); weekly
		// generation is the idempotent one. Merges into one row either way.
		tx, err := db.Begin()
		if err != nil {
			WriteServerError(w, r, err)
			return
		}
		defer tx.Rollback() //nolint:errcheck
		added, merged := 0, 0
		for _, a := range additions {
			mr, err := services.MergeIntoShoppingList(tx, a)
			if err != nil {
				WriteServerError(w, r, err)
				return
			}
			if mr.Created {
				added++
			} else {
				merged++
			}
		}
		if err := tx.Commit(); err != nil {
			WriteServerError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"added": added, "merged": merged})
		if added+merged > 0 {
			if wsHub != nil {
				wsHub.Broadcast("shopping_updated")
			}
		}
	})
}

// ingredientProductID decides which product a recipe ingredient links to: an
// explicit product_id, else a legacy inventory_id (a stock lot id), else the
// product with the same name, else none.
func ingredientProductID(q querier, productID, lotID *int64, name string) *int64 {
	if productID != nil {
		if _, err := getProduct(q, *productID); err == nil {
			return productID
		}
	}
	if pid := optProductID(q, lotID); pid != nil {
		return pid
	}
	return findProductIDByName(q, name)
}

func getRecipeWithIngredients(db *sql.DB, id int64) (map[string]any, error) {
	row := db.QueryRow(`SELECT id,name,description,instructions,tags,servings,archived FROM recipes WHERE id=?`, id)
	var rid int64
	var name, desc, instructions, tags string
	var servings, archived int
	if err := row.Scan(&rid, &name, &desc, &instructions, &tags, &servings, &archived); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id,recipe_id,product_id,name,quantity,unit FROM recipe_ingredients WHERE recipe_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ings []map[string]any
	for rows.Next() {
		var iid, recID int64
		var invID sql.NullInt64
		var iname, iunit string
		var qty float64
		if err := rows.Scan(&iid, &recID, &invID, &iname, &qty, &iunit); err != nil {
			return nil, err
		}
		var invIDVal any
		if invID.Valid {
			invIDVal = invID.Int64
		}
		ings = append(ings, map[string]any{
			"id": iid, "recipe_id": recID, "product_id": invIDVal,
			"name": iname, "quantity": qty, "unit": iunit,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if ings == nil {
		ings = []map[string]any{}
	}
	return map[string]any{
		"id": rid, "name": name, "description": desc,
		"instructions": instructions, "tags": tags, "servings": servings,
		"ingredients": ings, "archived": archived != 0,
	}, nil
}

type recipeAvailability struct {
	Available    bool
	Incompatible []string // ingredients whose unit can't be compared to stock
}

func recipeIsAvailable(db *sql.DB, recipe map[string]any) (recipeAvailability, error) {
	res := recipeAvailability{}
	ings := recipe["ingredients"].([]map[string]any)
	if len(ings) == 0 {
		return res, nil
	}
	available := true
	for _, ing := range ings {
		pid, hasPID := ing["product_id"].(int64)
		if !hasPID {
			available = false
			continue
		}
		invQty, invUnit, err := productStock(db, pid)
		if err == sql.ErrNoRows {
			available = false
			continue
		}
		if err != nil {
			return res, err
		}
		ingQty := ing["quantity"].(float64)
		ingUnit := units.Unit(ing["unit"].(string))

		converted, err := units.Convert(ingQty, ingUnit, units.Unit(invUnit))
		if err != nil {
			name, _ := ing["name"].(string)
			res.Incompatible = append(res.Incompatible, name)
			available = false
			continue
		}
		if invQty < converted {
			available = false
		}
	}
	res.Available = available
	return res, nil
}
