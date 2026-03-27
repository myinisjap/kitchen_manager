package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
)

func RegisterRecipes(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /api/recipes/", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name         string  `json:"name"`
			Description  string  `json:"description"`
			Instructions string  `json:"instructions"`
			Tags         string  `json:"tags"`
			Servings     int     `json:"servings"`
			Ingredients  []struct {
				InventoryID *int64  `json:"inventory_id"`
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
		res, err := db.Exec(`INSERT INTO recipes (name,description,instructions,tags,servings) VALUES (?,?,?,?,?)`,
			input.Name, input.Description, input.Instructions, input.Tags, input.Servings)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		recipeID, _ := res.LastInsertId()
		for _, ing := range input.Ingredients {
			if _, err := db.Exec(`INSERT INTO recipe_ingredients (recipe_id,inventory_id,name,quantity,unit) VALUES (?,?,?,?,?)`,
				recipeID, ing.InventoryID, ing.Name, ing.Quantity, ing.Unit); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		recipe, err := getRecipeWithIngredients(db, recipeID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, recipe)
	})

	mux.HandleFunc("GET /api/recipes/", func(w http.ResponseWriter, r *http.Request) {
		tag := r.URL.Query().Get("tag")
		availableOnly := r.URL.Query().Get("available_only") == "true"

		rows, err := db.Query(`SELECT id,name,description,instructions,tags,servings FROM recipes`)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Collect all matching recipe IDs first, then close cursor before calling
		// getRecipeWithIngredients (which opens its own queries).
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
				WriteError(w, http.StatusInternalServerError, err.Error())
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
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows.Close()

		var result []map[string]any
		for _, c := range candidates {
			recipe, err := getRecipeWithIngredients(db, c.id)
			if err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			available, err := recipeIsAvailable(db, recipe)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if availableOnly && !available {
			continue
		}
			result = append(result, recipe)
		}
		if result == nil {
			result = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, result)
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
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, recipe)
	})

	mux.HandleFunc("DELETE /api/recipes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		// Check existence first
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM recipes WHERE id=?`, id).Scan(&exists); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if exists == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		tx, err := db.Begin()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := tx.Exec(`DELETE FROM recipe_ingredients WHERE recipe_id=?`, id); err != nil {
			tx.Rollback()
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := tx.Exec(`DELETE FROM recipes WHERE id=?`, id); err != nil {
			tx.Rollback()
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.Commit(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/recipes/{id}/add-to-shopping-list", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		servingsParam := r.URL.Query().Get("servings")
		requestedServings := 1
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
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		recipeServings := recipe["servings"].(int)
		if recipeServings == 0 {
			recipeServings = 1
		}
		scale := float64(requestedServings) / float64(recipeServings)
		ings := recipe["ingredients"].([]map[string]any)
		added := 0
		for _, ing := range ings {
			needed := ing["quantity"].(float64) * scale
			have := 0.0
			if invID, ok := ing["inventory_id"]; ok && invID != nil {
				db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, invID).Scan(&have)
			}
			shortfall := needed - have
			if shortfall > 0 {
				if _, err := db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,'recipe')`,
					ing["inventory_id"], ing["name"], shortfall, ing["unit"]); err != nil {
					WriteError(w, http.StatusInternalServerError, err.Error())
					return
				}
				added++
			}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"added": added})
	})
}

func getRecipeWithIngredients(db *sql.DB, id int64) (map[string]any, error) {
	row := db.QueryRow(`SELECT id,name,description,instructions,tags,servings FROM recipes WHERE id=?`, id)
	var rid int64
	var name, desc, instructions, tags string
	var servings int
	if err := row.Scan(&rid, &name, &desc, &instructions, &tags, &servings); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT id,recipe_id,inventory_id,name,quantity,unit FROM recipe_ingredients WHERE recipe_id=?`, id)
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
			"id": iid, "recipe_id": recID, "inventory_id": invIDVal,
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
		"ingredients": ings,
	}, nil
}

func recipeIsAvailable(db *sql.DB, recipe map[string]any) (bool, error) {
	ings := recipe["ingredients"].([]map[string]any)
	for _, ing := range ings {
		invID, hasInvID := ing["inventory_id"]
		if !hasInvID || invID == nil {
			continue
		}
		var qty float64
		err := db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, invID).Scan(&qty)
		if err == sql.ErrNoRows {
			// Ingredient links to an inventory item that doesn't exist → not available
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if qty < ing["quantity"].(float64) {
			return false, nil
		}
	}
	return true, nil
}
