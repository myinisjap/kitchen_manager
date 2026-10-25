package services

import (
	"database/sql"
	"sort"
	"strings"
)

type UseItUpRecipe struct {
	RecipeID       int64    `json:"recipe_id"`
	Name           string   `json:"name"`
	Servings       int      `json:"servings"`
	ExpiringCount  int      `json:"expiring_count"`
	ExpiringItems  []string `json:"expiring_items"`
	EarliestExpiry string   `json:"earliest_expiry"`
}

// RankUseItUp ranks non-archived recipes by the number of distinct expiring
// products (quantity > 0, expiry <= cutoff) their ingredients use.
func RankUseItUp(db *sql.DB, cutoff string, limit int) ([]UseItUpRecipe, error) {
	rows, err := db.Query(`
WITH exp AS (
  SELECT p.id AS pid, LOWER(TRIM(p.name)) AS lname, p.name AS name, MIN(l.expiration_date) AS exp_date
  FROM stock_lots l JOIN products p ON p.id = l.product_id
  WHERE l.expiration_date != '' AND l.expiration_date <= ? AND l.quantity > 0
  GROUP BY p.id
)
SELECT DISTINCT r.id, r.name, r.servings, exp.lname, exp.name, exp.exp_date
FROM recipes r
JOIN recipe_ingredients ri ON ri.recipe_id = r.id
JOIN exp ON exp.pid = ri.product_id OR exp.lname = LOWER(TRIM(ri.name))
WHERE r.archived = 0`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type item struct{ name, date string }
	byID := map[int64]*UseItUpRecipe{}
	items := map[int64]map[string]item{}
	for rows.Next() {
		var id int64
		var name, lname, pname, date string
		var servings int
		if err := rows.Scan(&id, &name, &servings, &lname, &pname, &date); err != nil {
			return nil, err
		}
		if byID[id] == nil {
			byID[id] = &UseItUpRecipe{RecipeID: id, Name: name, Servings: servings}
			items[id] = map[string]item{}
		}
		items[id][lname] = item{pname, date}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []UseItUpRecipe{}
	for id, r := range byID {
		list := make([]item, 0, len(items[id]))
		for _, it := range items[id] {
			list = append(list, it)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].date != list[j].date {
				return list[i].date < list[j].date
			}
			return list[i].name < list[j].name
		})
		r.ExpiringCount = len(list)
		r.EarliestExpiry = list[0].date
		r.ExpiringItems = make([]string, len(list))
		for i, it := range list {
			r.ExpiringItems[i] = it.name
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ExpiringCount != b.ExpiringCount {
			return a.ExpiringCount > b.ExpiringCount
		}
		if a.EarliestExpiry != b.EarliestExpiry {
			return a.EarliestExpiry < b.EarliestExpiry
		}
		an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if an != bn {
			return an < bn
		}
		return a.RecipeID < b.RecipeID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
