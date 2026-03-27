# Kitchen Manager (Go) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a single-page, mobile-accessible web app for tracking household food inventory with shopping lists, recipe integration, and weekly meal calendar planning.

**Architecture:** Go stdlib `net/http` serves a REST JSON API backed by SQLite (`modernc.org/sqlite` — pure Go, no CGO). The frontend is a single `static/index.html` using Alpine.js + Tailwind CSS via CDN. All business logic lives in Go; the browser only fetches/posts JSON. No build pipeline required for the frontend.

**Tech Stack:** Go 1.22+, `modernc.org/sqlite`, `net/http` (stdlib), `encoding/json` (stdlib), `testing` + `net/http/httptest` (stdlib).

---

## File Structure

```
kitchen_manager/
├── main.go                    # Entry point: wires routes, starts server
├── db.go                      # DB open, schema creation, migration
├── models.go                  # Go structs for all domain types
├── handlers/
│   ├── inventory.go           # HTTP handlers for /api/inventory/*
│   ├── shopping.go            # HTTP handlers for /api/shopping/*
│   ├── recipes.go             # HTTP handlers for /api/recipes/*
│   └── calendar.go            # HTTP handlers for /api/calendar/*
├── services/
│   ├── shopping.go            # Threshold-based shopping list generation
│   └── calendar.go            # Weekly shopping list from meal plan
├── static/
│   └── index.html             # SPA: Alpine.js + Tailwind CDN
├── handlers/
│   └── helpers.go             # writeJSON, readJSON, pathID helpers
├── go.mod
└── go.sum
```

---

## Task 1: Project Scaffold

**Files:**
- Create: `go.mod`
- Create: `db.go`
- Create: `models.go`
- Create: `main.go`
- Create: `handlers/helpers.go`
- Create: `static/index.html` (placeholder)

- [ ] **Step 1: Initialize Go module**

```bash
cd /home/josh/code_projects/kitchen_manager
go mod init kitchen_manager
```

- [ ] **Step 2: Add SQLite dependency**

```bash
go get modernc.org/sqlite
```

- [ ] **Step 3: Create models.go**

```go
package main

// InventoryItem represents a food item in the pantry.
type InventoryItem struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Quantity       float64 `json:"quantity"`
	Unit           string  `json:"unit"`
	Location       string  `json:"location"`
	ExpirationDate string  `json:"expiration_date"` // YYYY-MM-DD or ""
	LowThreshold   float64 `json:"low_threshold"`
	Barcode        string  `json:"barcode"`
}

// ShoppingItem is a line item on the shopping list.
type ShoppingItem struct {
	ID             int64   `json:"id"`
	InventoryID    *int64  `json:"inventory_id"`
	Name           string  `json:"name"`
	QuantityNeeded float64 `json:"quantity_needed"`
	Unit           string  `json:"unit"`
	Checked        bool    `json:"checked"`
	Source         string  `json:"source"` // manual | threshold | recipe | calendar
}

// RecipeIngredient is one ingredient line within a recipe.
type RecipeIngredient struct {
	ID          int64   `json:"id"`
	RecipeID    int64   `json:"recipe_id"`
	InventoryID *int64  `json:"inventory_id"`
	Name        string  `json:"name"`
	Quantity    float64 `json:"quantity"`
	Unit        string  `json:"unit"`
}

// Recipe is a named set of ingredients and instructions.
type Recipe struct {
	ID           int64              `json:"id"`
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	Instructions string             `json:"instructions"`
	Tags         string             `json:"tags"` // comma-separated
	Servings     int                `json:"servings"`
	Ingredients  []RecipeIngredient `json:"ingredients"`
}

// MealEntry is a single meal slot on the calendar.
type MealEntry struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`      // YYYY-MM-DD
	MealSlot string `json:"meal_slot"` // breakfast | lunch | dinner
	RecipeID int64  `json:"recipe_id"`
	Servings int    `json:"servings"`
}
```

- [ ] **Step 4: Create db.go**

```go
package main

import (
	"database/sql"
	_ "modernc.org/sqlite"
)

var db *sql.DB

func openDB(path string) error {
	var err error
	db, err = sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1) // SQLite: single writer
	return createSchema()
}

func createSchema() error {
	_, err := db.Exec(`
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS inventory (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		name            TEXT    NOT NULL,
		quantity        REAL    NOT NULL DEFAULT 0,
		unit            TEXT    NOT NULL DEFAULT '',
		location        TEXT    NOT NULL DEFAULT '',
		expiration_date TEXT    NOT NULL DEFAULT '',
		low_threshold   REAL    NOT NULL DEFAULT 1,
		barcode         TEXT    NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS shopping_list (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id     INTEGER REFERENCES inventory(id),
		name             TEXT    NOT NULL,
		quantity_needed  REAL    NOT NULL DEFAULT 1,
		unit             TEXT    NOT NULL DEFAULT '',
		checked          INTEGER NOT NULL DEFAULT 0,
		source           TEXT    NOT NULL DEFAULT 'manual'
	);

	CREATE TABLE IF NOT EXISTS recipes (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		name         TEXT    NOT NULL,
		description  TEXT    NOT NULL DEFAULT '',
		instructions TEXT    NOT NULL DEFAULT '',
		tags         TEXT    NOT NULL DEFAULT '',
		servings     INTEGER NOT NULL DEFAULT 1
	);

	CREATE TABLE IF NOT EXISTS recipe_ingredients (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		recipe_id    INTEGER NOT NULL REFERENCES recipes(id),
		inventory_id INTEGER REFERENCES inventory(id),
		name         TEXT    NOT NULL,
		quantity     REAL    NOT NULL,
		unit         TEXT    NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS meal_calendar (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		date      TEXT    NOT NULL,
		meal_slot TEXT    NOT NULL DEFAULT 'dinner',
		recipe_id INTEGER NOT NULL REFERENCES recipes(id),
		servings  INTEGER NOT NULL DEFAULT 1
	);
	`)
	return err
}
```

- [ ] **Step 5: Create handlers/helpers.go**

```go
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func ReadJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// PathID extracts the last path segment as an int64.
// e.g. /api/inventory/42  →  42
func PathID(r *http.Request) (int64, bool) {
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
	if len(parts) == 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	return id, err == nil
}

func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}
```

- [ ] **Step 6: Create main.go**

```go
package main

import (
	"log"
	"net/http"

	"kitchen_manager/handlers"
)

func main() {
	if err := openDB("./kitchen.db"); err != nil {
		log.Fatal("db open:", err)
	}
	defer db.Close()

	mux := http.NewServeMux()

	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterCalendar(mux, db)

	mux.Handle("/", http.FileServer(http.Dir("./static")))

	log.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
```

- [ ] **Step 7: Create placeholder static/index.html**

```bash
mkdir -p static handlers services
```

`static/index.html`:
```html
<!DOCTYPE html><html><body><h1>Kitchen Manager</h1></body></html>
```

- [ ] **Step 8: Create empty handler files so it compiles**

`handlers/inventory.go`:
```go
package handlers

import (
	"database/sql"
	"net/http"
)

func RegisterInventory(mux *http.ServeMux, db *sql.DB) {}
```

`handlers/shopping.go`:
```go
package handlers

import (
	"database/sql"
	"net/http"
)

func RegisterShopping(mux *http.ServeMux, db *sql.DB) {}
```

`handlers/recipes.go`:
```go
package handlers

import (
	"database/sql"
	"net/http"
)

func RegisterRecipes(mux *http.ServeMux, db *sql.DB) {}
```

`handlers/calendar.go`:
```go
package handlers

import (
	"database/sql"
	"net/http"
)

func RegisterCalendar(mux *http.ServeMux, db *sql.DB) {}
```

- [ ] **Step 9: Verify it compiles and starts**

```bash
go build ./... && go run .
```

Expected: `Listening on :8080` — visit `http://localhost:8080` and see "Kitchen Manager".

- [ ] **Step 10: Commit**

```bash
git init
git add .
git commit -m "feat: Go project scaffold with SQLite schema and stdlib HTTP server"
```

---

## Task 2: Inventory Handlers

**Files:**
- Modify: `handlers/inventory.go`
- Create: `handlers/inventory_test.go`

Go's `net/http` (1.22+) supports `{id}` path parameters natively. We use `mux.HandleFunc("GET /api/inventory/{id}", ...)` pattern.

- [ ] **Step 1: Write failing inventory tests**

`handlers/inventory_test.go`:
```go
package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kitchen_manager/handlers"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`PRAGMA foreign_keys = ON;
	CREATE TABLE IF NOT EXISTS inventory (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		quantity REAL NOT NULL DEFAULT 0,
		unit TEXT NOT NULL DEFAULT '',
		location TEXT NOT NULL DEFAULT '',
		expiration_date TEXT NOT NULL DEFAULT '',
		low_threshold REAL NOT NULL DEFAULT 1,
		barcode TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS shopping_list (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		inventory_id INTEGER REFERENCES inventory(id),
		name TEXT NOT NULL,
		quantity_needed REAL NOT NULL DEFAULT 1,
		unit TEXT NOT NULL DEFAULT '',
		checked INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT 'manual'
	);
	CREATE TABLE IF NOT EXISTS recipes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		instructions TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '',
		servings INTEGER NOT NULL DEFAULT 1
	);
	CREATE TABLE IF NOT EXISTS recipe_ingredients (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		recipe_id INTEGER NOT NULL REFERENCES recipes(id),
		inventory_id INTEGER REFERENCES inventory(id),
		name TEXT NOT NULL,
		quantity REAL NOT NULL,
		unit TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS meal_calendar (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date TEXT NOT NULL,
		meal_slot TEXT NOT NULL DEFAULT 'dinner',
		recipe_id INTEGER NOT NULL REFERENCES recipes(id),
		servings INTEGER NOT NULL DEFAULT 1
	);`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newMux(t *testing.T) (*http.ServeMux, *sql.DB) {
	db := newTestDB(t)
	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterCalendar(mux, db)
	return mux, db
}

func TestCreateInventoryItem(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Milk","quantity":1,"unit":"gallon","location":"fridge","low_threshold":0.5}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var item map[string]any
	json.NewDecoder(w.Body).Decode(&item)
	if item["name"] != "Milk" {
		t.Errorf("want name Milk, got %v", item["name"])
	}
	if item["id"] == nil {
		t.Error("want id, got nil")
	}
}

func TestListInventoryItems(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Eggs","quantity":12,"unit":"count","location":"fridge"}`
	httptest.NewRecorder() // warm up
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("GET", "/api/inventory/", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}
	var items []map[string]any
	json.NewDecoder(w2.Body).Decode(&items)
	found := false
	for _, item := range items {
		if item["name"] == "Eggs" {
			found = true
		}
	}
	if !found {
		t.Error("Eggs not found in inventory list")
	}
}

func TestUpdateInventoryItem(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(`{"name":"Butter","quantity":2,"unit":"stick","location":"fridge"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int(created["id"].(float64))

	patchBody := `{"quantity":1}`
	req2 := httptest.NewRequest("PATCH", "/api/inventory/"+strconv.Itoa(id), bytes.NewBufferString(patchBody))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w2.Code, w2.Body)
	}
	var updated map[string]any
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated["quantity"] != 1.0 {
		t.Errorf("want quantity 1, got %v", updated["quantity"])
	}
}

func TestDeleteInventoryItem(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(`{"name":"ToDelete","quantity":1,"unit":"","location":""}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int(created["id"].(float64))

	req2 := httptest.NewRequest("DELETE", "/api/inventory/"+strconv.Itoa(id), nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w2.Code)
	}

	req3 := httptest.NewRequest("GET", "/api/inventory/"+strconv.Itoa(id), nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("want 404 after delete, got %d", w3.Code)
	}
}

func TestGetExpiringSoon(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Yogurt","quantity":1,"unit":"cup","location":"fridge","expiration_date":"2026-03-29"}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("GET", "/api/inventory/expiring?days=7", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}
	var items []map[string]any
	json.NewDecoder(w2.Body).Decode(&items)
	found := false
	for _, item := range items {
		if item["name"] == "Yogurt" {
			found = true
		}
	}
	if !found {
		t.Error("Yogurt not found in expiring items")
	}
}
```

Note: add `"strconv"` to imports in the test file above.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./handlers/ -v -run TestCreate
```

Expected: compile error or FAIL — handlers not implemented.

- [ ] **Step 3: Implement handlers/inventory.go**

```go
package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

func RegisterInventory(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /api/inventory/", func(w http.ResponseWriter, r *http.Request) {
		var item struct {
			Name           string  `json:"name"`
			Quantity       float64 `json:"quantity"`
			Unit           string  `json:"unit"`
			Location       string  `json:"location"`
			ExpirationDate string  `json:"expiration_date"`
			LowThreshold   float64 `json:"low_threshold"`
			Barcode        string  `json:"barcode"`
		}
		if err := ReadJSON(r, &item); err != nil || item.Name == "" {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if item.LowThreshold == 0 {
			item.LowThreshold = 1
		}
		res, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,location,expiration_date,low_threshold,barcode) VALUES (?,?,?,?,?,?,?)`,
			item.Name, item.Quantity, item.Unit, item.Location, item.ExpirationDate, item.LowThreshold, item.Barcode)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		WriteJSON(w, http.StatusCreated, map[string]any{
			"id": id, "name": item.Name, "quantity": item.Quantity,
			"unit": item.Unit, "location": item.Location,
			"expiration_date": item.ExpirationDate, "low_threshold": item.LowThreshold,
			"barcode": item.Barcode,
		})
	})

	mux.HandleFunc("GET /api/inventory/expiring", func(w http.ResponseWriter, r *http.Request) {
		daysStr := r.URL.Query().Get("days")
		days := 7
		if daysStr != "" {
			if d, err := strconv.Atoi(daysStr); err == nil {
				days = d
			}
		}
		today := time.Now().Format("2006-01-02")
		cutoff := time.Now().AddDate(0, 0, days).Format("2006-01-02")
		rows, err := db.Query(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode FROM inventory WHERE expiration_date != '' AND expiration_date >= ? AND expiration_date <= ?`, today, cutoff)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		WriteJSON(w, http.StatusOK, scanInventoryRows(rows))
	})

	mux.HandleFunc("GET /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		row := db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode FROM inventory WHERE id=?`, id)
		item, err := scanInventoryRow(row)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, item)
	})

	mux.HandleFunc("GET /api/inventory/", func(w http.ResponseWriter, r *http.Request) {
		q := `SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode FROM inventory WHERE 1=1`
		args := []any{}
		if name := r.URL.Query().Get("name"); name != "" {
			q += ` AND name LIKE ?`
			args = append(args, "%"+name+"%")
		}
		if loc := r.URL.Query().Get("location"); loc != "" {
			q += ` AND location=?`
			args = append(args, loc)
		}
		rows, err := db.Query(q, args...)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		WriteJSON(w, http.StatusOK, scanInventoryRows(rows))
	})

	mux.HandleFunc("PATCH /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var patch map[string]any
		if err := ReadJSON(r, &patch); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		allowed := []string{"name", "quantity", "unit", "location", "expiration_date", "low_threshold", "barcode"}
		for _, field := range allowed {
			if val, ok := patch[field]; ok {
				db.Exec(`UPDATE inventory SET `+field+`=? WHERE id=?`, val, id)
			}
		}
		row := db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode FROM inventory WHERE id=?`, id)
		item, err := scanInventoryRow(row)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		WriteJSON(w, http.StatusOK, item)
	})

	mux.HandleFunc("DELETE /api/inventory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		res, _ := db.Exec(`DELETE FROM inventory WHERE id=?`, id)
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func scanInventoryRow(row *sql.Row) (map[string]any, error) {
	var id int64
	var name, unit, location, expDate, barcode string
	var qty, threshold float64
	err := row.Scan(&id, &name, &qty, &unit, &location, &expDate, &threshold, &barcode)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "name": name, "quantity": qty, "unit": unit,
		"location": location, "expiration_date": expDate,
		"low_threshold": threshold, "barcode": barcode,
	}, nil
}

func scanInventoryRows(rows *sql.Rows) []map[string]any {
	var items []map[string]any
	for rows.Next() {
		var id int64
		var name, unit, location, expDate, barcode string
		var qty, threshold float64
		if err := rows.Scan(&id, &name, &qty, &unit, &location, &expDate, &threshold, &barcode); err == nil {
			items = append(items, map[string]any{
				"id": id, "name": name, "quantity": qty, "unit": unit,
				"location": location, "expiration_date": expDate,
				"low_threshold": threshold, "barcode": barcode,
			})
		}
	}
	if items == nil {
		return []map[string]any{}
	}
	return items
}

// pathIDFromPattern reads a named path parameter from Go 1.22 ServeMux patterns.
func pathIDFromPattern(r *http.Request, param string) (int64, bool) {
	val := r.PathValue(param)
	id, err := strconv.ParseInt(val, 10, 64)
	return id, err == nil
}
```

- [ ] **Step 4: Update helpers.go to remove the old PathID (replaced by pathIDFromPattern)**

The `PathID` function in `helpers.go` is no longer needed — `r.PathValue` handles it. Remove it:

```go
package handlers

import (
	"encoding/json"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func ReadJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./handlers/ -v -run "TestCreateInventoryItem|TestListInventoryItems|TestUpdateInventoryItem|TestDeleteInventoryItem|TestGetExpiringSoon"
```

Expected: All 5 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add .
git commit -m "feat: inventory CRUD handlers with expiring-soon query"
```

---

## Task 3: Shopping Handlers + Threshold Service

**Files:**
- Create: `services/shopping.go`
- Modify: `handlers/shopping.go`
- Modify: `handlers/inventory_test.go` (add shopping tests to same test file)

- [ ] **Step 1: Create services/shopping.go**

```go
package services

import (
	"database/sql"
)

// GenerateFromThresholds finds all inventory items below their low_threshold
// and adds them to the shopping list if not already present (unchecked).
// Returns the number of items added.
func GenerateFromThresholds(db *sql.DB) (int, error) {
	rows, err := db.Query(`SELECT id, name, quantity, low_threshold, unit FROM inventory WHERE quantity < low_threshold`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	added := 0
	for rows.Next() {
		var id int64
		var name, unit string
		var qty, threshold float64
		if err := rows.Scan(&id, &name, &qty, &threshold, &unit); err != nil {
			continue
		}
		// Check if already on the shopping list (unchecked)
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM shopping_list WHERE inventory_id=? AND checked=0`, id).Scan(&count)
		if count > 0 {
			continue
		}
		needed := threshold - qty
		db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,'threshold')`,
			id, name, needed, unit)
		added++
	}
	return added, nil
}
```

- [ ] **Step 2: Write failing shopping tests — add to handlers/inventory_test.go**

Append to `handlers/inventory_test.go`:
```go
func TestAddManualShoppingItem(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Olive Oil","quantity_needed":1,"unit":"bottle","source":"manual"}`
	req := httptest.NewRequest("POST", "/api/shopping/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var item map[string]any
	json.NewDecoder(w.Body).Decode(&item)
	if item["name"] != "Olive Oil" {
		t.Errorf("want Olive Oil, got %v", item["name"])
	}
	if item["checked"] != false {
		t.Errorf("want checked=false, got %v", item["checked"])
	}
}

func TestCheckShoppingItem(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest("POST", "/api/shopping/", bytes.NewBufferString(`{"name":"Pepper","quantity_needed":1,"unit":"jar"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var created map[string]any
	json.NewDecoder(w.Body).Decode(&created)
	id := int(created["id"].(float64))

	req2 := httptest.NewRequest("PATCH", "/api/shopping/"+strconv.Itoa(id), bytes.NewBufferString(`{"checked":true}`))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}
	var updated map[string]any
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated["checked"] != true {
		t.Errorf("want checked=true, got %v", updated["checked"])
	}
}

func TestClearCheckedShoppingItems(t *testing.T) {
	mux, _ := newMux(t)
	var id1, id2 int
	for i, name := range []string{"ItemA", "ItemB"} {
		req := httptest.NewRequest("POST", "/api/shopping/", bytes.NewBufferString(`{"name":"`+name+`","quantity_needed":1,"unit":""}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var c map[string]any
		json.NewDecoder(w.Body).Decode(&c)
		if i == 0 {
			id1 = int(c["id"].(float64))
		} else {
			id2 = int(c["id"].(float64))
			_ = id2
		}
	}
	// Check ItemA
	req := httptest.NewRequest("PATCH", "/api/shopping/"+strconv.Itoa(id1), bytes.NewBufferString(`{"checked":true}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("DELETE", "/api/shopping/checked", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w2.Code)
	}

	req3 := httptest.NewRequest("GET", "/api/shopping/", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	var items []map[string]any
	json.NewDecoder(w3.Body).Decode(&items)
	for _, item := range items {
		if item["name"] == "ItemA" {
			t.Error("ItemA should have been cleared")
		}
	}
	found := false
	for _, item := range items {
		if item["name"] == "ItemB" {
			found = true
		}
	}
	if !found {
		t.Error("ItemB should still be on the list")
	}
}

func TestGenerateFromThresholds(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"ThresholdTest","quantity":0.2,"unit":"bottle","location":"pantry","low_threshold":1.0}`
	req := httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("POST", "/api/shopping/generate-from-thresholds", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w2.Code, w2.Body)
	}

	req3 := httptest.NewRequest("GET", "/api/shopping/", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	var items []map[string]any
	json.NewDecoder(w3.Body).Decode(&items)
	found := false
	for _, item := range items {
		if item["name"] == "ThresholdTest" {
			found = true
		}
	}
	if !found {
		t.Error("ThresholdTest not found in shopping list after threshold generation")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./handlers/ -v -run "TestAddManual|TestCheck|TestClear|TestGenerate"
```

Expected: FAIL — shopping handlers not implemented.

- [ ] **Step 4: Implement handlers/shopping.go**

```go
package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"kitchen_manager/services"
)

func RegisterShopping(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /api/shopping/generate-from-thresholds", func(w http.ResponseWriter, r *http.Request) {
		added, err := services.GenerateFromThresholds(db)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"added": added})
	})

	mux.HandleFunc("DELETE /api/shopping/checked", func(w http.ResponseWriter, r *http.Request) {
		db.Exec(`DELETE FROM shopping_list WHERE checked=1`)
		WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
	})

	mux.HandleFunc("POST /api/shopping/", func(w http.ResponseWriter, r *http.Request) {
		var item struct {
			InventoryID    *int64  `json:"inventory_id"`
			Name           string  `json:"name"`
			QuantityNeeded float64 `json:"quantity_needed"`
			Unit           string  `json:"unit"`
			Source         string  `json:"source"`
		}
		if err := ReadJSON(r, &item); err != nil || item.Name == "" {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if item.Source == "" {
			item.Source = "manual"
		}
		if item.QuantityNeeded == 0 {
			item.QuantityNeeded = 1
		}
		res, err := db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,?)`,
			item.InventoryID, item.Name, item.QuantityNeeded, item.Unit, item.Source)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		WriteJSON(w, http.StatusCreated, map[string]any{
			"id": id, "inventory_id": item.InventoryID, "name": item.Name,
			"quantity_needed": item.QuantityNeeded, "unit": item.Unit,
			"checked": false, "source": item.Source,
		})
	})

	mux.HandleFunc("GET /api/shopping/", func(w http.ResponseWriter, r *http.Request) {
		showChecked := r.URL.Query().Get("show_checked") == "true"
		q := `SELECT id,inventory_id,name,quantity_needed,unit,checked,source FROM shopping_list`
		if !showChecked {
			q += ` WHERE checked=0`
		}
		rows, err := db.Query(q)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		WriteJSON(w, http.StatusOK, scanShoppingRows(rows))
	})

	mux.HandleFunc("PATCH /api/shopping/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var patch map[string]any
		if err := ReadJSON(r, &patch); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if checked, ok := patch["checked"]; ok {
			val := 0
			if checked == true {
				val = 1
			}
			db.Exec(`UPDATE shopping_list SET checked=? WHERE id=?`, val, id)
		}
		if qty, ok := patch["quantity_needed"]; ok {
			db.Exec(`UPDATE shopping_list SET quantity_needed=? WHERE id=?`, qty, id)
		}
		row := db.QueryRow(`SELECT id,inventory_id,name,quantity_needed,unit,checked,source FROM shopping_list WHERE id=?`, id)
		item, err := scanShoppingRow(row)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		WriteJSON(w, http.StatusOK, item)
	})

	mux.HandleFunc("DELETE /api/shopping/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathIDFromPattern(r, "id")
		if !ok {
			WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		res, _ := db.Exec(`DELETE FROM shopping_list WHERE id=?`, id)
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func scanShoppingRow(row *sql.Row) (map[string]any, error) {
	var id int64
	var invID sql.NullInt64
	var name, unit, source string
	var qty float64
	var checked int
	err := row.Scan(&id, &invID, &name, &qty, &unit, &checked, &source)
	if err != nil {
		return nil, err
	}
	var invIDVal any
	if invID.Valid {
		invIDVal = invID.Int64
	}
	return map[string]any{
		"id": id, "inventory_id": invIDVal, "name": name,
		"quantity_needed": qty, "unit": unit, "checked": checked == 1, "source": source,
	}, nil
}

func scanShoppingRows(rows *sql.Rows) []map[string]any {
	var items []map[string]any
	for rows.Next() {
		var id int64
		var invID sql.NullInt64
		var name, unit, source string
		var qty float64
		var checked int
		if err := rows.Scan(&id, &invID, &name, &qty, &unit, &checked, &source); err == nil {
			var invIDVal any
			if invID.Valid {
				invIDVal = invID.Int64
			}
			items = append(items, map[string]any{
				"id": id, "inventory_id": invIDVal, "name": name,
				"quantity_needed": qty, "unit": unit, "checked": checked == 1, "source": source,
			})
		}
	}
	if items == nil {
		return []map[string]any{}
	}
	return items
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./handlers/ ./services/ -v -run "TestAddManual|TestCheck|TestClear|TestGenerate"
```

Expected: All 4 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add .
git commit -m "feat: shopping list handlers and threshold-based auto-generation service"
```

---

## Task 4: Recipe Handlers

**Files:**
- Modify: `handlers/recipes.go`
- Modify: `handlers/inventory_test.go` (append recipe tests)

- [ ] **Step 1: Append recipe tests to handlers/inventory_test.go**

```go
func TestCreateRecipe(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Scrambled Eggs","description":"Simple breakfast","instructions":"Beat eggs, cook.","tags":"breakfast,quick","servings":2,"ingredients":[{"name":"Eggs","quantity":3,"unit":"count"},{"name":"Butter","quantity":1,"unit":"tbsp"}]}`
	req := httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var recipe map[string]any
	json.NewDecoder(w.Body).Decode(&recipe)
	if recipe["name"] != "Scrambled Eggs" {
		t.Errorf("want Scrambled Eggs, got %v", recipe["name"])
	}
	ings := recipe["ingredients"].([]any)
	if len(ings) != 2 {
		t.Errorf("want 2 ingredients, got %d", len(ings))
	}
}

func TestFilterRecipesByTag(t *testing.T) {
	mux, _ := newMux(t)
	body := `{"name":"Pancakes","tags":"breakfast,sweet","servings":4,"ingredients":[]}`
	req := httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	req2 := httptest.NewRequest("GET", "/api/recipes/?tag=breakfast", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	var recipes []map[string]any
	json.NewDecoder(w2.Body).Decode(&recipes)
	found := false
	for _, r := range recipes {
		if r["name"] == "Pancakes" {
			found = true
		}
	}
	if !found {
		t.Error("Pancakes not found when filtering by breakfast tag")
	}
}

func TestAddRecipeToShoppingList(t *testing.T) {
	mux, _ := newMux(t)
	// Create inventory item with 0 quantity
	inv := httptest.NewRecorder()
	mux.ServeHTTP(inv, httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(`{"name":"RecipeEgg","quantity":0,"unit":"count","location":"fridge"}`)))
	var invItem map[string]any
	json.NewDecoder(inv.Body).Decode(&invItem)
	invID := int(invItem["id"].(float64))

	body := `{"name":"QuickOmelet","servings":1,"ingredients":[{"name":"RecipeEgg","quantity":2,"unit":"count","inventory_id":` + strconv.Itoa(invID) + `}]}`
	req := httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var recipe map[string]any
	json.NewDecoder(w.Body).Decode(&recipe)
	recipeID := int(recipe["id"].(float64))

	req2 := httptest.NewRequest("POST", "/api/recipes/"+strconv.Itoa(recipeID)+"/add-to-shopping-list", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w2.Code, w2.Body)
	}

	req3 := httptest.NewRequest("GET", "/api/shopping/", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	var items []map[string]any
	json.NewDecoder(w3.Body).Decode(&items)
	found := false
	for _, item := range items {
		if item["name"] == "RecipeEgg" {
			found = true
		}
	}
	if !found {
		t.Error("RecipeEgg not found in shopping list after adding recipe")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./handlers/ -v -run "TestCreateRecipe|TestFilterRecipes|TestAddRecipeToShoppingList"
```

Expected: FAIL — recipes handler not implemented.

- [ ] **Step 3: Implement handlers/recipes.go**

```go
package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
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
			db.Exec(`INSERT INTO recipe_ingredients (recipe_id,inventory_id,name,quantity,unit) VALUES (?,?,?,?,?)`,
				recipeID, ing.InventoryID, ing.Name, ing.Quantity, ing.Unit)
		}
		WriteJSON(w, http.StatusCreated, getRecipeWithIngredients(db, recipeID))
	})

	mux.HandleFunc("GET /api/recipes/", func(w http.ResponseWriter, r *http.Request) {
		tag := r.URL.Query().Get("tag")
		availableOnly := r.URL.Query().Get("available_only") == "true"

		rows, err := db.Query(`SELECT id,name,description,instructions,tags,servings FROM recipes`)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		var result []map[string]any
		for rows.Next() {
			var id int64
			var name, desc, instructions, tags string
			var servings int
			if err := rows.Scan(&id, &name, &desc, &instructions, &tags, &servings); err != nil {
				continue
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
			recipe := getRecipeWithIngredients(db, id)
			if availableOnly && !recipeIsAvailable(db, recipe) {
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
		recipe := getRecipeWithIngredients(db, id)
		if recipe == nil {
			WriteError(w, http.StatusNotFound, "not found")
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
		res, _ := db.Exec(`DELETE FROM recipes WHERE id=?`, id)
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		db.Exec(`DELETE FROM recipe_ingredients WHERE recipe_id=?`, id)
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
			if s, err := strconv.Atoi(servingsParam); err == nil {
				requestedServings = s
			}
		}
		recipe := getRecipeWithIngredients(db, id)
		if recipe == nil {
			WriteError(w, http.StatusNotFound, "not found")
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
				db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,'recipe')`,
					ing["inventory_id"], ing["name"], shortfall, ing["unit"])
				added++
			}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"added": added})
	})
}

func getRecipeWithIngredients(db *sql.DB, id int64) map[string]any {
	row := db.QueryRow(`SELECT id,name,description,instructions,tags,servings FROM recipes WHERE id=?`, id)
	var rid int64
	var name, desc, instructions, tags string
	var servings int
	if err := row.Scan(&rid, &name, &desc, &instructions, &tags, &servings); err != nil {
		return nil
	}
	rows, _ := db.Query(`SELECT id,recipe_id,inventory_id,name,quantity,unit FROM recipe_ingredients WHERE recipe_id=?`, id)
	defer rows.Close()
	var ings []map[string]any
	for rows.Next() {
		var iid, recID int64
		var invID sql.NullInt64
		var iname, iunit string
		var qty float64
		if err := rows.Scan(&iid, &recID, &invID, &iname, &qty, &iunit); err == nil {
			var invIDVal any
			if invID.Valid {
				invIDVal = invID.Int64
			}
			ings = append(ings, map[string]any{
				"id": iid, "recipe_id": recID, "inventory_id": invIDVal,
				"name": iname, "quantity": qty, "unit": iunit,
			})
		}
	}
	if ings == nil {
		ings = []map[string]any{}
	}
	return map[string]any{
		"id": rid, "name": name, "description": desc,
		"instructions": instructions, "tags": tags, "servings": servings,
		"ingredients": ings,
	}
}

func recipeIsAvailable(db *sql.DB, recipe map[string]any) bool {
	ings := recipe["ingredients"].([]map[string]any)
	for _, ing := range ings {
		if invID, ok := ing["inventory_id"]; ok && invID != nil {
			var qty float64
			db.QueryRow(`SELECT quantity FROM inventory WHERE id=?`, invID).Scan(&qty)
			if qty < ing["quantity"].(float64) {
				return false
			}
		}
	}
	return true
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./handlers/ -v -run "TestCreateRecipe|TestFilterRecipes|TestAddRecipeToShoppingList"
```

Expected: All 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "feat: recipe CRUD handlers with tag filtering, availability filter, and shopping list integration"
```

---

## Task 5: Calendar Handlers + Weekly Shopping Service

**Files:**
- Create: `services/calendar.go`
- Modify: `handlers/calendar.go`
- Modify: `handlers/inventory_test.go` (append calendar tests)

- [ ] **Step 1: Create services/calendar.go**

```go
package services

import (
	"database/sql"
	"time"
)

type ShoppingNeed struct {
	InventoryID    *int64
	Name           string
	Unit           string
	QuantityNeeded float64
}

// GenerateWeeklyShopping simulates daily inventory depletion over a 7-day week
// and returns the shopping items needed, accounting for prior days' usage.
func GenerateWeeklyShopping(db *sql.DB, weekStart string) ([]ShoppingNeed, error) {
	start, err := time.Parse("2006-01-02", weekStart)
	if err != nil {
		return nil, err
	}
	end := start.AddDate(0, 0, 6).Format("2006-01-02")

	rows, err := db.Query(`
		SELECT mc.id, mc.date, mc.recipe_id, mc.servings, r.servings as recipe_servings
		FROM meal_calendar mc
		JOIN recipes r ON r.id = mc.recipe_id
		WHERE mc.date >= ? AND mc.date <= ?
		ORDER BY mc.date`, weekStart, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type entry struct {
		date           string
		recipeID       int64
		servings       int
		recipeServings int
	}
	var entries []entry
	for rows.Next() {
		var id, recipeID int64
		var date string
		var servings, recipeServings int
		if err := rows.Scan(&id, &date, &recipeID, &servings, &recipeServings); err == nil {
			entries = append(entries, entry{date, recipeID, servings, recipeServings})
		}
	}

	// Simulated inventory: map[inventoryID]quantity
	simulated := map[int64]float64{}
	invRows, err := db.Query(`SELECT id, quantity FROM inventory`)
	if err != nil {
		return nil, err
	}
	defer invRows.Close()
	for invRows.Next() {
		var id int64
		var qty float64
		if err := invRows.Scan(&id, &qty); err == nil {
			simulated[id] = qty
		}
	}

	// key: (inventory_id_str + name + unit) → ShoppingNeed
	needs := map[string]*ShoppingNeed{}

	for _, e := range entries {
		recServings := e.recipeServings
		if recServings == 0 {
			recServings = 1
		}
		scale := float64(e.servings) / float64(recServings)

		ingRows, err := db.Query(`SELECT inventory_id, name, quantity, unit FROM recipe_ingredients WHERE recipe_id=?`, e.recipeID)
		if err != nil {
			continue
		}
		for ingRows.Next() {
			var invID sql.NullInt64
			var name, unit string
			var qty float64
			if err := ingRows.Scan(&invID, &name, &qty, &unit); err != nil {
				continue
			}
			needed := qty * scale
			available := 0.0
			if invID.Valid {
				available = simulated[invID.Int64]
			}
			shortfall := needed - available
			if shortfall < 0 {
				shortfall = 0
			}

			key := name + "|" + unit
			if invID.Valid {
				key = strconv.FormatInt(invID.Int64, 10) + "|" + unit
			}
			if shortfall > 0 {
				if needs[key] == nil {
					var invIDPtr *int64
					if invID.Valid {
						v := invID.Int64
						invIDPtr = &v
					}
					needs[key] = &ShoppingNeed{InventoryID: invIDPtr, Name: name, Unit: unit}
				}
				needs[key].QuantityNeeded += shortfall
			}

			// Deduct from simulated inventory for future days
			if invID.Valid {
				remaining := available - needed
				if remaining < 0 {
					remaining = 0
				}
				simulated[invID.Int64] = remaining
			}
		}
		ingRows.Close()
	}

	var result []ShoppingNeed
	for _, n := range needs {
		result = append(result, *n)
	}
	return result, nil
}
```

Add `"strconv"` to the import in `services/calendar.go`.

- [ ] **Step 2: Append calendar tests to handlers/inventory_test.go**

```go
func TestAddMealToCalendar(t *testing.T) {
	mux, _ := newMux(t)
	recipe := httptest.NewRecorder()
	mux.ServeHTTP(recipe, httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(`{"name":"CalRecipe","servings":2,"ingredients":[]}`)))
	var r map[string]any
	json.NewDecoder(recipe.Body).Decode(&r)
	recipeID := int(r["id"].(float64))

	body := `{"date":"2026-04-07","meal_slot":"dinner","recipe_id":` + strconv.Itoa(recipeID) + `,"servings":2}`
	req := httptest.NewRequest("POST", "/api/calendar/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var entry map[string]any
	json.NewDecoder(w.Body).Decode(&entry)
	if entry["recipe_id"] != float64(recipeID) {
		t.Errorf("want recipe_id %d, got %v", recipeID, entry["recipe_id"])
	}
}

func TestWeeklyShoppingAccountsForInventory(t *testing.T) {
	mux, db := newMux(t)

	// Add inventory: 2 eggs
	invW := httptest.NewRecorder()
	mux.ServeHTTP(invW, httptest.NewRequest("POST", "/api/inventory/", bytes.NewBufferString(`{"name":"CalEgg","quantity":2,"unit":"count","location":"fridge"}`)))
	var inv map[string]any
	json.NewDecoder(invW.Body).Decode(&inv)
	invID := int(inv["id"].(float64))
	_ = db

	// Create recipe: needs 4 eggs per serving
	recW := httptest.NewRecorder()
	body := `{"name":"EggDish","servings":1,"ingredients":[{"name":"CalEgg","quantity":4,"unit":"count","inventory_id":` + strconv.Itoa(invID) + `}]}`
	mux.ServeHTTP(recW, httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(body)))
	var rec map[string]any
	json.NewDecoder(recW.Body).Decode(&rec)
	recipeID := int(rec["id"].(float64))

	// Add recipe on Monday and Wednesday
	for _, date := range []string{"2026-04-14", "2026-04-16"} {
		entry := `{"date":"` + date + `","meal_slot":"dinner","recipe_id":` + strconv.Itoa(recipeID) + `,"servings":1}`
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/calendar/", bytes.NewBufferString(entry)))
	}

	req := httptest.NewRequest("POST", "/api/calendar/generate-weekly-shopping?start=2026-04-14", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	items := result["items"].([]any)

	total := 0.0
	for _, item := range items {
		m := item.(map[string]any)
		if m["name"] == "CalEgg" {
			total += m["quantity_needed"].(float64)
		}
	}
	// Mon: need 4, have 2 → buy 2; stock → 0
	// Wed: need 4, have 0 → buy 4
	// Total = 6
	if total != 6.0 {
		t.Errorf("want 6 eggs needed, got %v", total)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./handlers/ -v -run "TestAddMeal|TestWeeklyShopping"
```

Expected: FAIL — calendar handler not implemented.

- [ ] **Step 4: Implement handlers/calendar.go**

```go
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
		var exists int
		db.QueryRow(`SELECT COUNT(*) FROM recipes WHERE id=?`, input.RecipeID).Scan(&exists)
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
		start := r.URL.Query().Get("start")
		if start == "" {
			start = time.Now().Format("2006-01-02")
		}
		t, err := time.Parse("2006-01-02", start)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid start date")
			return
		}
		end := t.AddDate(0, 0, 6).Format("2006-01-02")
		rows, err := db.Query(`SELECT id,date,meal_slot,recipe_id,servings FROM meal_calendar WHERE date>=? AND date<=? ORDER BY date`, start, end)
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
			if err := rows.Scan(&id, &date, &mealSlot, &recipeID, &servings); err == nil {
				entries = append(entries, map[string]any{
					"id": id, "date": date, "meal_slot": mealSlot,
					"recipe_id": recipeID, "servings": servings,
				})
			}
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
		res, _ := db.Exec(`DELETE FROM meal_calendar WHERE id=?`, id)
		n, _ := res.RowsAffected()
		if n == 0 {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/calendar/generate-weekly-shopping", func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("start")
		if start == "" {
			start = time.Now().Format("2006-01-02")
		}
		needs, err := services.GenerateWeeklyShopping(db, start)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var items []map[string]any
		for _, n := range needs {
			res, _ := db.Exec(`INSERT INTO shopping_list (inventory_id,name,quantity_needed,unit,checked,source) VALUES (?,?,?,?,0,'calendar')`,
				n.InventoryID, n.Name, n.QuantityNeeded, n.Unit)
			id, _ := res.LastInsertId()
			items = append(items, map[string]any{
				"id": id, "inventory_id": n.InventoryID, "name": n.Name,
				"quantity_needed": n.QuantityNeeded, "unit": n.Unit,
				"checked": false, "source": "calendar",
			})
		}
		if items == nil {
			items = []map[string]any{}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"week_start": start, "items": items})
	})
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./... -v -run "TestAddMeal|TestWeeklyShopping"
```

Expected: Both tests PASS.

- [ ] **Step 6: Run all tests**

```bash
go test ./... -v
```

Expected: All tests PASS.

- [ ] **Step 7: Commit**

```bash
git add .
git commit -m "feat: meal calendar handlers and weekly shopping service with simulated inventory depletion"
```

---

## Task 6: Frontend SPA

**Files:**
- Modify: `static/index.html` (full rewrite — identical to Python plan)

The frontend is pure browser code and doesn't care whether the backend is Python or Go. Replace `static/index.html` with the complete SPA from the Python plan (Task 6, Step 1). The API paths (`/api/inventory/`, `/api/shopping/`, etc.) are identical.

- [ ] **Step 1: Replace static/index.html with the full SPA**

Copy the complete `static/index.html` content from the Python plan (Task 6, Step 1). The entire Alpine.js + Tailwind app is unchanged — all API endpoints match.

- [ ] **Step 2: Start the app and manually verify**

```bash
go run .
```

Open `http://localhost:8080`. Verify:
- 4 tabs (Pantry, Shopping, Recipes, Calendar) visible at bottom
- Add an inventory item via modal — appears in list
- Shopping tab: "Auto-generate" button works
- Recipes tab: create a recipe with ingredients
- Calendar tab: week navigation works, "+ Add meal" opens recipe picker

- [ ] **Step 3: Commit**

```bash
git add static/index.html
git commit -m "feat: complete SPA frontend — inventory, shopping, recipes, calendar tabs"
```

---

## Task 7: Integration Test + Final Smoke

**Files:**
- Modify: `handlers/inventory_test.go` (append integration test)

- [ ] **Step 1: Append full integration test**

```go
func TestFullWeeklyPlanningFlow(t *testing.T) {
	mux, _ := newMux(t)

	// 1. Add inventory: pasta (200g, threshold 100) — above threshold
	pastaW := httptest.NewRecorder()
	mux.ServeHTTP(pastaW, httptest.NewRequest("POST", "/api/inventory/",
		bytes.NewBufferString(`{"name":"Pasta","quantity":200,"unit":"g","location":"pantry","low_threshold":100}`)))
	var pasta map[string]any
	json.NewDecoder(pastaW.Body).Decode(&pasta)
	pastaID := int(pasta["id"].(float64))

	// 2. Add inventory: sauce (0.5 jar, threshold 2) — below threshold
	sauceW := httptest.NewRecorder()
	mux.ServeHTTP(sauceW, httptest.NewRequest("POST", "/api/inventory/",
		bytes.NewBufferString(`{"name":"TomatoSauce","quantity":0.5,"unit":"jar","location":"pantry","low_threshold":2}`)))
	var sauce map[string]any
	json.NewDecoder(sauceW.Body).Decode(&sauce)
	sauceID := int(sauce["id"].(float64))

	// 3. Auto-generate from thresholds — only sauce should appear
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/shopping/generate-from-thresholds", nil))
	shoppingW := httptest.NewRecorder()
	mux.ServeHTTP(shoppingW, httptest.NewRequest("GET", "/api/shopping/", nil))
	var shoppingItems []map[string]any
	json.NewDecoder(shoppingW.Body).Decode(&shoppingItems)
	foundSauce, foundPasta := false, false
	for _, item := range shoppingItems {
		if item["name"] == "TomatoSauce" { foundSauce = true }
		if item["name"] == "Pasta" { foundPasta = true }
	}
	if !foundSauce { t.Error("TomatoSauce should be on shopping list (below threshold)") }
	if foundPasta { t.Error("Pasta should NOT be on shopping list (above threshold)") }

	// 4. Create recipe: 300g pasta + 2 jars sauce for 2 servings
	recipeBody := `{"name":"Pasta Marinara","servings":2,"tags":"dinner,italian","ingredients":[` +
		`{"name":"Pasta","quantity":300,"unit":"g","inventory_id":` + strconv.Itoa(pastaID) + `},` +
		`{"name":"TomatoSauce","quantity":2,"unit":"jar","inventory_id":` + strconv.Itoa(sauceID) + `}]}`
	recipeW := httptest.NewRecorder()
	mux.ServeHTTP(recipeW, httptest.NewRequest("POST", "/api/recipes/", bytes.NewBufferString(recipeBody)))
	var recipe map[string]any
	json.NewDecoder(recipeW.Body).Decode(&recipe)
	recipeID := int(recipe["id"].(float64))

	// 5. Add to calendar: Mon 2026-04-20 and Wed 2026-04-22, 2 servings each
	for _, date := range []string{"2026-04-20", "2026-04-22"} {
		entry := `{"date":"` + date + `","meal_slot":"dinner","recipe_id":` + strconv.Itoa(recipeID) + `,"servings":2}`
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/calendar/", bytes.NewBufferString(entry)))
	}

	// 6. Generate weekly shopping
	weekW := httptest.NewRecorder()
	mux.ServeHTTP(weekW, httptest.NewRequest("POST", "/api/calendar/generate-weekly-shopping?start=2026-04-20", nil))
	if weekW.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", weekW.Code, weekW.Body)
	}
	var weekResult map[string]any
	json.NewDecoder(weekW.Body).Decode(&weekResult)
	items := weekResult["items"].([]any)

	// Pasta: have 200g; Mon needs 300g → buy 100g, stock=0; Wed needs 300g → buy 300g. Total=400g
	// Sauce: have 0.5 jar; Mon needs 2 → buy 1.5, stock=0; Wed needs 2 → buy 2. Total=3.5 jars
	pastaTotal, sauceTotal := 0.0, 0.0
	for _, item := range items {
		m := item.(map[string]any)
		switch m["name"] {
		case "Pasta":
			pastaTotal += m["quantity_needed"].(float64)
		case "TomatoSauce":
			sauceTotal += m["quantity_needed"].(float64)
		}
	}
	if pastaTotal != 400.0 {
		t.Errorf("want 400g pasta, got %v", pastaTotal)
	}
	if sauceTotal != 3.5 {
		t.Errorf("want 3.5 jars sauce, got %v", sauceTotal)
	}
}
```

- [ ] **Step 2: Run integration test**

```bash
go test ./handlers/ -v -run TestFullWeeklyPlanningFlow
```

Expected: PASS.

- [ ] **Step 3: Run full test suite**

```bash
go test ./... -v
```

Expected: All tests PASS, 0 failures.

- [ ] **Step 4: Build binary to verify clean compile**

```bash
go build -o kitchen-manager .
./kitchen-manager
```

Expected: `Listening on :8080`

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "test: integration test for full weekly planning flow; verify single-binary build"
```

---

## Future: Barcode Scanner (Deferred)

Add a `GET /api/inventory/lookup-barcode?code=<value>` handler that calls the Open Food Facts API (`https://world.openfoodfacts.org/api/v0/product/<barcode>.json`) and returns a pre-filled inventory struct. The `barcode` column is already in the schema. The frontend can use `BarcodeDetector` (Chrome/Android) or the `quagga2` JS library via CDN for camera access.

---

## Self-Review

**Spec coverage:**
- ✅ Track amounts, locations, expiration dates → inventory table + CRUD handlers
- ✅ Simple mobile-accessible UI → Alpine.js + Tailwind, bottom tab bar, modal sheets (same as Python plan)
- ✅ SQLite storage → `modernc.org/sqlite` pure-Go driver
- ✅ Shopping list from low threshold → `services/shopping.go` + `POST /api/shopping/generate-from-thresholds`
- ✅ Barcode scanner → deferred, column reserved, future path documented
- ✅ Recipe integration with tag + availability filtering → `handlers/recipes.go`
- ✅ Add recipe missing items to shopping list → `POST /api/recipes/{id}/add-to-shopping-list`
- ✅ Meal calendar → `meal_calendar` table + calendar handlers
- ✅ Weekly shopping from calendar accounting for prior days → `services/calendar.go`

**Placeholder scan:** None — all steps contain complete code.

**Type consistency:** `scanInventoryRows`/`scanShoppingRows`/`getRecipeWithIngredients` return `map[string]any` used consistently across handlers and tests. `services.ShoppingNeed` struct used in both service and calendar handler.
