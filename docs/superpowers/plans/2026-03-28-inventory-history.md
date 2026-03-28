# Inventory History and Statistics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a permanent inventory audit log and a History tab with activity, item stats, and top-items views.

**Architecture:** A new `inventory_history` SQLite table is written to by a `LogHistory` helper called inside the three existing inventory mutation handlers. Three new read endpoints aggregate and page the log. A fifth tab in the Alpine.js SPA renders the data in three sub-views.

**Tech Stack:** Go 1.22 stdlib HTTP, SQLite via modernc.org/sqlite, Alpine.js 3, Tailwind CSS (CDN).

---

## File Map

| Action | File | Responsibility |
|---|---|---|
| Modify | `db.go` | Add `inventory_history` table + indexes to `createSchema()` |
| Modify | `models.go` | Add `InventoryHistoryRow` struct |
| Create | `handlers/history.go` | `SessionManager` var, `LogHistory` helper, `RegisterHistory` with 3 routes |
| Modify | `handlers/inventory.go` | Call `LogHistory` in POST, PATCH, DELETE handlers |
| Modify | `main.go` | Call `handlers.RegisterHistory`; assign `handlers.SessionManager` |
| Modify | `handlers/inventory_test.go` | Add `inventory_history` table to `newTestDB`; add history tests |
| Modify | `static/index.html` | History tab section, 5 new state props, 7 new methods, updated tabs array, safelist |

---

## Task 1: Add `inventory_history` table to the schema

**Files:**
- Modify: `db.go`

- [ ] **Step 1: Add the table and indexes inside the existing `db.Exec` call in `createSchema()`**

  Inside the single `db.Exec(`` ` `` ... `` ` ``)` block in `createSchema()`, append after the `sessions` table and its index:

  ```sql
  CREATE TABLE IF NOT EXISTS inventory_history (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    inventory_id     INTEGER NOT NULL,
    item_name        TEXT    NOT NULL,
    changed_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    changed_by       TEXT,
    change_type      TEXT    NOT NULL,
    quantity_before  REAL,
    quantity_after   REAL,
    unit             TEXT    NOT NULL DEFAULT '',
    source           TEXT
  );

  CREATE INDEX IF NOT EXISTS inventory_history_item_idx
    ON inventory_history(inventory_id, changed_at);

  CREATE INDEX IF NOT EXISTS inventory_history_changed_at_idx
    ON inventory_history(changed_at);
  ```

- [ ] **Step 2: Verify the server starts cleanly**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go build ./... 2>&1
  ```

  Expected: no output (clean build).

- [ ] **Step 3: Commit**

  ```bash
  git add db.go
  git commit -m "feat: add inventory_history table to schema"
  ```

---

## Task 2: Add `InventoryHistoryRow` to models

**Files:**
- Modify: `models.go`

- [ ] **Step 1: Append the struct to `models.go`**

  Add at the end of `models.go`:

  ```go
  // InventoryHistoryRow is one entry in the inventory audit log.
  type InventoryHistoryRow struct {
  	ID             int64    `json:"id"`
  	InventoryID    int64    `json:"inventory_id"`
  	ItemName       string   `json:"item_name"`
  	ChangedAt      string   `json:"changed_at"`
  	ChangedBy      *string  `json:"changed_by"`
  	ChangeType     string   `json:"change_type"`
  	QuantityBefore *float64 `json:"quantity_before"`
  	QuantityAfter  *float64 `json:"quantity_after"`
  	Unit           string   `json:"unit"`
  	Source         *string  `json:"source"`
  }
  ```

- [ ] **Step 2: Verify build**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go build ./...
  ```

  Expected: no output.

- [ ] **Step 3: Commit**

  ```bash
  git add models.go
  git commit -m "feat: add InventoryHistoryRow model"
  ```

---

## Task 3: Create `handlers/history.go` with `LogHistory` and `RegisterHistory`

**Files:**
- Create: `handlers/history.go`

- [ ] **Step 1: Write the failing test for `LogHistory` being callable**

  In `handlers/inventory_test.go`, add after `newMux`:

  ```go
  func newMuxWithHistory(t *testing.T) (*http.ServeMux, *sql.DB) {
  	db := newTestDB(t)
  	mux := http.NewServeMux()
  	handlers.RegisterInventory(mux, db)
  	handlers.RegisterShopping(mux, db)
  	handlers.RegisterRecipes(mux, db)
  	handlers.RegisterCalendar(mux, db)
  	handlers.RegisterHistory(mux, db)
  	return mux, db
  }

  func TestHistoryTableExistsInTestDB(t *testing.T) {
  	_, db := newMuxWithHistory(t)
  	_, err := db.Exec(`INSERT INTO inventory_history
  		(inventory_id, item_name, change_type, unit)
  		VALUES (1, 'Test', 'create', 'g')`)
  	if err != nil {
  		t.Fatalf("inventory_history table not accessible: %v", err)
  	}
  }
  ```

  Also update `newTestDB` to include the `inventory_history` table:

  ```go
  // Add inside the db.Exec schema string in newTestDB, after the sessions block:
  `CREATE TABLE IF NOT EXISTS inventory_history (
  	id               INTEGER PRIMARY KEY AUTOINCREMENT,
  	inventory_id     INTEGER NOT NULL,
  	item_name        TEXT    NOT NULL,
  	changed_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
  	changed_by       TEXT,
  	change_type      TEXT    NOT NULL,
  	quantity_before  REAL,
  	quantity_after   REAL,
  	unit             TEXT    NOT NULL DEFAULT '',
  	source           TEXT
  );`
  ```

- [ ] **Step 2: Run the test to confirm it fails**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go test ./handlers/... -run TestHistoryTableExistsInTestDB -v 2>&1
  ```

  Expected: compile error — `handlers.RegisterHistory undefined`.

- [ ] **Step 3: Create `handlers/history.go`**

  ```go
  package handlers

  import (
  	"context"
  	"database/sql"
  	"math"
  	"net/http"
  	"strconv"
  )

  // SessionManager is set from main.go when OAuth is enabled.
  // It must satisfy the interface used by scs.SessionManager.
  var SessionManager interface {
  	GetString(context.Context, string) string
  }

  // LogHistory writes one row to inventory_history.
  // A failure is non-fatal: callers should log the error but not abort the response.
  func LogHistory(
  	db *sql.DB,
  	r *http.Request,
  	inventoryID int64,
  	itemName string,
  	changeType string,
  	quantityBefore, quantityAfter *float64,
  	unit, source string,
  ) error {
  	var changedBy *string
  	if SessionManager != nil {
  		email := SessionManager.GetString(r.Context(), "email")
  		if email != "" {
  			changedBy = &email
  		}
  	}
  	var src *string
  	if source != "" {
  		src = &source
  	}
  	_, err := db.Exec(
  		`INSERT INTO inventory_history
  			(inventory_id, item_name, changed_by, change_type,
  			 quantity_before, quantity_after, unit, source)
  			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
  		inventoryID, itemName, changedBy, changeType,
  		quantityBefore, quantityAfter, unit, src,
  	)
  	return err
  }

  func RegisterHistory(mux *http.ServeMux, db *sql.DB) {
  	// GET /api/history — paginated full log
  	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
  		limit := 100
  		offset := 0
  		if s := r.URL.Query().Get("limit"); s != "" {
  			v, err := strconv.Atoi(s)
  			if err != nil || v < 0 {
  				WriteError(w, http.StatusBadRequest, "invalid limit")
  				return
  			}
  			if v > 500 {
  				v = 500
  			}
  			limit = v
  		}
  		if s := r.URL.Query().Get("offset"); s != "" {
  			v, err := strconv.Atoi(s)
  			if err != nil || v < 0 {
  				WriteError(w, http.StatusBadRequest, "invalid offset")
  				return
  			}
  			offset = v
  		}

  		var total int
  		if err := db.QueryRow(`SELECT COUNT(*) FROM inventory_history`).Scan(&total); err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}

  		rows, err := db.Query(
  			`SELECT id, inventory_id, item_name, changed_at, changed_by,
  				change_type, quantity_before, quantity_after, unit, source
  			FROM inventory_history
  			ORDER BY changed_at DESC, id DESC
  			LIMIT ? OFFSET ?`,
  			limit, offset,
  		)
  		if err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}
  		defer rows.Close()
  		result, err := scanHistoryRows(rows)
  		if err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}
  		WriteJSON(w, http.StatusOK, map[string]any{
  			"rows":   result,
  			"total":  total,
  			"limit":  limit,
  			"offset": offset,
  		})
  	})

  	// GET /api/history/item/{id} — history for one item
  	mux.HandleFunc("GET /api/history/item/{id}", func(w http.ResponseWriter, r *http.Request) {
  		id, ok := pathIDFromPattern(r, "id")
  		if !ok {
  			WriteError(w, http.StatusBadRequest, "invalid id")
  			return
  		}
  		rows, err := db.Query(
  			`SELECT id, inventory_id, item_name, changed_at, changed_by,
  				change_type, quantity_before, quantity_after, unit, source
  			FROM inventory_history
  			WHERE inventory_id = ?
  			ORDER BY changed_at DESC, id DESC`,
  			id,
  		)
  		if err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}
  		defer rows.Close()
  		result, err := scanHistoryRows(rows)
  		if err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}
  		WriteJSON(w, http.StatusOK, result)
  	})

  	// GET /api/history/stats — aggregate stats
  	mux.HandleFunc("GET /api/history/stats", func(w http.ResponseWriter, r *http.Request) {
  		type consumptionItem struct {
  			InventoryID int64   `json:"inventory_id"`
  			ItemName    string  `json:"item_name"`
  			Unit        string  `json:"unit"`
  			WeeklyRate  float64 `json:"weekly_rate"`
  		}
  		type topUsedItem struct {
  			InventoryID  int64   `json:"inventory_id"`
  			ItemName     string  `json:"item_name"`
  			Unit         string  `json:"unit"`
  			TotalRemoved float64 `json:"total_removed"`
  		}
  		type topRestockedItem struct {
  			InventoryID  int64  `json:"inventory_id"`
  			ItemName     string `json:"item_name"`
  			RestockCount int    `json:"restock_count"`
  		}

  		// Query 1: consumption rates
  		rateRows, err := db.Query(`
  			SELECT inventory_id, item_name, unit,
  			       SUM(quantity_before - quantity_after) / (30.0 / 7.0) AS weekly_rate
  			FROM inventory_history
  			WHERE change_type IN ('remove', 'delete')
  			  AND changed_at >= datetime('now', '-30 days')
  			  AND quantity_before IS NOT NULL
  			  AND quantity_after IS NOT NULL
  			GROUP BY inventory_id, item_name, unit
  			ORDER BY weekly_rate DESC`)
  		if err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}
  		defer rateRows.Close()
  		rates := []consumptionItem{}
  		for rateRows.Next() {
  			var item consumptionItem
  			if err := rateRows.Scan(&item.InventoryID, &item.ItemName, &item.Unit, &item.WeeklyRate); err != nil {
  				WriteError(w, http.StatusInternalServerError, err.Error())
  				return
  			}
  			item.WeeklyRate = math.Round(item.WeeklyRate*100) / 100
  			rates = append(rates, item)
  		}
  		if err := rateRows.Err(); err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}

  		// Query 2: top 5 most used
  		usedRows, err := db.Query(`
  			SELECT inventory_id, item_name, unit,
  			       SUM(quantity_before - quantity_after) AS total_removed
  			FROM inventory_history
  			WHERE change_type = 'remove'
  			  AND changed_at >= datetime('now', '-30 days')
  			  AND quantity_before IS NOT NULL
  			  AND quantity_after IS NOT NULL
  			GROUP BY inventory_id, item_name, unit
  			ORDER BY total_removed DESC
  			LIMIT 5`)
  		if err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}
  		defer usedRows.Close()
  		topUsed := []topUsedItem{}
  		for usedRows.Next() {
  			var item topUsedItem
  			if err := usedRows.Scan(&item.InventoryID, &item.ItemName, &item.Unit, &item.TotalRemoved); err != nil {
  				WriteError(w, http.StatusInternalServerError, err.Error())
  				return
  			}
  			item.TotalRemoved = math.Round(item.TotalRemoved*100) / 100
  			topUsed = append(topUsed, item)
  		}
  		if err := usedRows.Err(); err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}

  		// Query 3: top 5 most restocked
  		restockRows, err := db.Query(`
  			SELECT inventory_id, item_name, COUNT(*) AS restock_count
  			FROM inventory_history
  			WHERE change_type IN ('add', 'create')
  			  AND changed_at >= datetime('now', '-30 days')
  			GROUP BY inventory_id, item_name
  			ORDER BY restock_count DESC
  			LIMIT 5`)
  		if err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}
  		defer restockRows.Close()
  		topRestocked := []topRestockedItem{}
  		for restockRows.Next() {
  			var item topRestockedItem
  			if err := restockRows.Scan(&item.InventoryID, &item.ItemName, &item.RestockCount); err != nil {
  				WriteError(w, http.StatusInternalServerError, err.Error())
  				return
  			}
  			topRestocked = append(topRestocked, item)
  		}
  		if err := restockRows.Err(); err != nil {
  			WriteError(w, http.StatusInternalServerError, err.Error())
  			return
  		}

  		WriteJSON(w, http.StatusOK, map[string]any{
  			"consumption_rates": rates,
  			"top_used":          topUsed,
  			"top_restocked":     topRestocked,
  		})
  	})
  }

  func scanHistoryRows(rows *sql.Rows) ([]InventoryHistoryRow, error) {
  	var result []InventoryHistoryRow
  	for rows.Next() {
  		var row InventoryHistoryRow
  		if err := rows.Scan(
  			&row.ID, &row.InventoryID, &row.ItemName, &row.ChangedAt,
  			&row.ChangedBy, &row.ChangeType,
  			&row.QuantityBefore, &row.QuantityAfter,
  			&row.Unit, &row.Source,
  		); err != nil {
  			return nil, err
  		}
  		result = append(result, row)
  	}
  	if err := rows.Err(); err != nil {
  		return nil, err
  	}
  	if result == nil {
  		return []InventoryHistoryRow{}, nil
  	}
  	return result, nil
  }
  ```

  Note: `scanHistoryRows` uses `InventoryHistoryRow` from `package main` — but `handlers` is a separate package. The type needs to be either redefined in `handlers` or the handler can return `[]map[string]any`. The cleanest approach matching the existing codebase pattern (where `scanInventoryRow` returns `map[string]any`) is to use a local unexported struct or inline struct. Use an inline approach in `scanHistoryRows`:

  Replace the `InventoryHistoryRow` struct usage in `scanHistoryRows` with:
  ```go
  type historyRow struct {
  	ID             int64    `json:"id"`
  	InventoryID    int64    `json:"inventory_id"`
  	ItemName       string   `json:"item_name"`
  	ChangedAt      string   `json:"changed_at"`
  	ChangedBy      *string  `json:"changed_by"`
  	ChangeType     string   `json:"change_type"`
  	QuantityBefore *float64 `json:"quantity_before"`
  	QuantityAfter  *float64 `json:"quantity_after"`
  	Unit           string   `json:"unit"`
  	Source         *string  `json:"source"`
  }
  ```
  declared at the top of `history.go` (unexported, within the package). Remove `InventoryHistoryRow` from `models.go` (it was added in Task 2 but the handlers package cannot import `main`). The `models.go` struct is not needed — remove it.

- [ ] **Step 4: Run the test**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go test ./handlers/... -run TestHistoryTableExistsInTestDB -v 2>&1
  ```

  Expected: `PASS`.

- [ ] **Step 5: Run all existing tests to confirm nothing is broken**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go test ./handlers/... -v 2>&1 | tail -20
  ```

  Expected: all tests PASS, no FAILs.

- [ ] **Step 6: Commit**

  ```bash
  git add handlers/history.go handlers/inventory_test.go models.go
  git commit -m "feat: add LogHistory helper and RegisterHistory with 3 endpoints"
  ```

---

## Task 4: Wire `RegisterHistory` and `SessionManager` in `main.go`

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Add `handlers.RegisterHistory` call**

  In `main.go`, after `handlers.RegisterCalendar(mux, db)`, add:

  ```go
  handlers.RegisterHistory(mux, db)
  ```

- [ ] **Step 2: Assign `handlers.SessionManager` inside the OAuth block**

  In `main.go`, inside the `if os.Getenv("OAUTH_ENABLED") == "true"` block, immediately after `sessionManager = newSessionManager()`, add:

  ```go
  handlers.SessionManager = sessionManager
  ```

- [ ] **Step 3: Build and verify**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go build ./... 2>&1
  ```

  Expected: no output.

- [ ] **Step 4: Commit**

  ```bash
  git add main.go
  git commit -m "feat: register history routes and wire SessionManager"
  ```

---

## Task 5: Call `LogHistory` in inventory mutation handlers

**Files:**
- Modify: `handlers/inventory.go`

- [ ] **Step 1: Write the failing tests**

  In `handlers/inventory_test.go`, add these three tests. They use `newMuxWithHistory` (added in Task 3).

  ```go
  func TestHistoryLoggedOnCreate(t *testing.T) {
  	mux, db := newMuxWithHistory(t)
  	body := `{"name":"Eggs","quantity":12,"unit":"piece","location":"fridge"}`
  	req := httptest.NewRequest(http.MethodPost, "/api/inventory/", bytes.NewBufferString(body))
  	req.Header.Set("Content-Type", "application/json")
  	rr := httptest.NewRecorder()
  	mux.ServeHTTP(rr, req)
  	if rr.Code != http.StatusCreated {
  		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
  	}
  	var count int
  	if err := db.QueryRow(`SELECT COUNT(*) FROM inventory_history WHERE item_name='Eggs' AND change_type='create'`).Scan(&count); err != nil {
  		t.Fatal(err)
  	}
  	if count != 1 {
  		t.Errorf("expected 1 history row for create, got %d", count)
  	}
  }

  func TestHistoryLoggedOnPatch(t *testing.T) {
  	mux, db := newMuxWithHistory(t)
  	// Create item
  	body := `{"name":"Milk","quantity":2,"unit":"L","location":"fridge"}`
  	req := httptest.NewRequest(http.MethodPost, "/api/inventory/", bytes.NewBufferString(body))
  	req.Header.Set("Content-Type", "application/json")
  	rr := httptest.NewRecorder()
  	mux.ServeHTTP(rr, req)
  	var created map[string]any
  	json.NewDecoder(rr.Body).Decode(&created)
  	id := int64(created["id"].(float64))

  	// Patch quantity down (remove)
  	patch := `{"quantity":1}`
  	req2 := httptest.NewRequest(http.MethodPatch, "/api/inventory/"+strconv.FormatInt(id, 10), bytes.NewBufferString(patch))
  	req2.Header.Set("Content-Type", "application/json")
  	rr2 := httptest.NewRecorder()
  	mux.ServeHTTP(rr2, req2)
  	if rr2.Code != http.StatusOK {
  		t.Fatalf("expected 200, got %d", rr2.Code)
  	}
  	var changeType string
  	if err := db.QueryRow(`SELECT change_type FROM inventory_history WHERE item_name='Milk' AND change_type IN ('add','remove','edit') ORDER BY id DESC LIMIT 1`).Scan(&changeType); err != nil {
  		t.Fatalf("no patch history row: %v", err)
  	}
  	if changeType != "remove" {
  		t.Errorf("expected change_type 'remove', got %q", changeType)
  	}
  }

  func TestHistoryLoggedOnDelete(t *testing.T) {
  	mux, db := newMuxWithHistory(t)
  	body := `{"name":"Butter","quantity":1,"unit":"kg","location":"fridge"}`
  	req := httptest.NewRequest(http.MethodPost, "/api/inventory/", bytes.NewBufferString(body))
  	req.Header.Set("Content-Type", "application/json")
  	rr := httptest.NewRecorder()
  	mux.ServeHTTP(rr, req)
  	var created map[string]any
  	json.NewDecoder(rr.Body).Decode(&created)
  	id := int64(created["id"].(float64))

  	req2 := httptest.NewRequest(http.MethodDelete, "/api/inventory/"+strconv.FormatInt(id, 10), nil)
  	rr2 := httptest.NewRecorder()
  	mux.ServeHTTP(rr2, req2)
  	if rr2.Code != http.StatusNoContent {
  		t.Fatalf("expected 204, got %d", rr2.Code)
  	}
  	var count int
  	if err := db.QueryRow(`SELECT COUNT(*) FROM inventory_history WHERE item_name='Butter' AND change_type='delete'`).Scan(&count); err != nil {
  		t.Fatal(err)
  	}
  	if count != 1 {
  		t.Errorf("expected 1 history row for delete, got %d", count)
  	}
  }
  ```

- [ ] **Step 2: Run tests to confirm they fail**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go test ./handlers/... -run "TestHistoryLoggedOn" -v 2>&1
  ```

  Expected: 3 FAILs — no history rows are written yet.

- [ ] **Step 3: Add `LogHistory` call to `POST /api/inventory/`**

  In `handlers/inventory.go`, inside the `POST /api/inventory/` handler, immediately after `id, _ := res.LastInsertId()` and before `WriteJSON(...)`:

  ```go
  qAfter := item.Quantity
  if err := LogHistory(db, r, id, item.Name, "create", nil, &qAfter, item.Unit, "manual"); err != nil {
  	// history failure is non-fatal
  	_ = err
  }
  ```

- [ ] **Step 4: Add `LogHistory` call to `DELETE /api/inventory/{id}`**

  In `handlers/inventory.go`, inside the `DELETE /api/inventory/{id}` handler, before the existing `res, _ := db.Exec(...)` DELETE, add a pre-fetch:

  ```go
  var delName, delUnit string
  var delQty float64
  _ = db.QueryRow(`SELECT name, quantity, unit FROM inventory WHERE id=?`, id).Scan(&delName, &delQty, &delUnit)
  ```

  After `n, _ := res.RowsAffected()` and inside `if n == 0 { ... }` / after it, before `w.WriteHeader(http.StatusNoContent)`:

  ```go
  if err := LogHistory(db, r, id, delName, "delete", &delQty, nil, delUnit, "manual"); err != nil {
  	_ = err
  }
  ```

- [ ] **Step 5: Add `LogHistory` call to `PATCH /api/inventory/{id}`**

  In `handlers/inventory.go`, inside the `PATCH /api/inventory/{id}` handler:

  **a.** After `id, ok := pathIDFromPattern(r, "id")` and `var patch map[string]any` + `ReadJSON(...)`, add a pre-fetch before the validation block:

  ```go
  var preName, preUnit string
  var preQty float64
  _ = db.QueryRow(`SELECT name, quantity, unit FROM inventory WHERE id=?`, id).Scan(&preName, &preQty, &preUnit)
  ```

  **b.** After `WriteJSON(w, http.StatusOK, item)` and the `scanInventoryRow` call (which produces `item`), insert before `WriteJSON`:

  ```go
  // Determine change_type for history
  postQty, _ := item["quantity"].(float64)
  source := r.URL.Query().Get("source")
  if source == "" {
  	source = "manual"
  }
  changeType := "edit"
  if len(patch) == 1 {
  	if _, onlyQty := patch["quantity"]; onlyQty {
  		if postQty > preQty {
  			changeType = "add"
  		} else {
  			changeType = "remove"
  		}
  	}
  }
  if err := LogHistory(db, r, id, preName, changeType, &preQty, &postQty, preUnit, source); err != nil {
  	_ = err
  }
  ```

- [ ] **Step 6: Run the three new tests**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go test ./handlers/... -run "TestHistoryLoggedOn" -v 2>&1
  ```

  Expected: 3 PASSes.

- [ ] **Step 7: Run all tests**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go test ./handlers/... -v 2>&1 | grep -E "^(---|\s*FAIL|ok)"
  ```

  Expected: `ok  	kitchen_manager/handlers` with no FAIL lines.

- [ ] **Step 8: Commit**

  ```bash
  git add handlers/inventory.go handlers/inventory_test.go
  git commit -m "feat: log history on inventory create/patch/delete"
  ```

---

## Task 6: Tests for the history API endpoints

**Files:**
- Modify: `handlers/inventory_test.go`

- [ ] **Step 1: Write tests for `GET /api/history`, `GET /api/history/item/{id}`, `GET /api/history/stats`**

  Add to `handlers/inventory_test.go`:

  ```go
  func TestGetHistoryPaginated(t *testing.T) {
  	mux, _ := newMuxWithHistory(t)
  	// Create two items to generate history rows
  	for _, name := range []string{"Apple", "Banana"} {
  		body := `{"name":"` + name + `","quantity":5,"unit":"piece","location":"pantry"}`
  		req := httptest.NewRequest(http.MethodPost, "/api/inventory/", bytes.NewBufferString(body))
  		req.Header.Set("Content-Type", "application/json")
  		mux.ServeHTTP(httptest.NewRecorder(), req)
  	}

  	req := httptest.NewRequest(http.MethodGet, "/api/history?limit=10&offset=0", nil)
  	rr := httptest.NewRecorder()
  	mux.ServeHTTP(rr, req)
  	if rr.Code != http.StatusOK {
  		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
  	}
  	var resp map[string]any
  	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
  		t.Fatal(err)
  	}
  	rows, ok := resp["rows"].([]any)
  	if !ok {
  		t.Fatal("rows field missing or wrong type")
  	}
  	if len(rows) < 2 {
  		t.Errorf("expected at least 2 history rows, got %d", len(rows))
  	}
  	if _, ok := resp["total"]; !ok {
  		t.Error("total field missing")
  	}
  }

  func TestGetHistoryInvalidParams(t *testing.T) {
  	mux, _ := newMuxWithHistory(t)
  	for _, url := range []string{"/api/history?limit=abc", "/api/history?offset=-1"} {
  		req := httptest.NewRequest(http.MethodGet, url, nil)
  		rr := httptest.NewRecorder()
  		mux.ServeHTTP(rr, req)
  		if rr.Code != http.StatusBadRequest {
  			t.Errorf("url %s: expected 400, got %d", url, rr.Code)
  		}
  	}
  }

  func TestGetHistoryForItem(t *testing.T) {
  	mux, _ := newMuxWithHistory(t)
  	body := `{"name":"Carrot","quantity":3,"unit":"piece","location":"fridge"}`
  	req := httptest.NewRequest(http.MethodPost, "/api/inventory/", bytes.NewBufferString(body))
  	req.Header.Set("Content-Type", "application/json")
  	rr := httptest.NewRecorder()
  	mux.ServeHTTP(rr, req)
  	var created map[string]any
  	json.NewDecoder(rr.Body).Decode(&created)
  	id := int64(created["id"].(float64))

  	req2 := httptest.NewRequest(http.MethodGet, "/api/history/item/"+strconv.FormatInt(id, 10), nil)
  	rr2 := httptest.NewRecorder()
  	mux.ServeHTTP(rr2, req2)
  	if rr2.Code != http.StatusOK {
  		t.Fatalf("expected 200, got %d: %s", rr2.Code, rr2.Body.String())
  	}
  	var rows []any
  	if err := json.NewDecoder(rr2.Body).Decode(&rows); err != nil {
  		t.Fatal(err)
  	}
  	if len(rows) != 1 {
  		t.Errorf("expected 1 row (create event), got %d", len(rows))
  	}
  }

  func TestGetHistoryStats(t *testing.T) {
  	mux, db := newMuxWithHistory(t)
  	// Seed a remove event directly
  	_, err := db.Exec(`INSERT INTO inventory_history
  		(inventory_id, item_name, change_type, quantity_before, quantity_after, unit, changed_at)
  		VALUES (1, 'Milk', 'remove', 2.0, 1.0, 'L', datetime('now', '-5 days'))`)
  	if err != nil {
  		t.Fatal(err)
  	}

  	req := httptest.NewRequest(http.MethodGet, "/api/history/stats", nil)
  	rr := httptest.NewRecorder()
  	mux.ServeHTTP(rr, req)
  	if rr.Code != http.StatusOK {
  		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
  	}
  	var stats map[string]any
  	if err := json.NewDecoder(rr.Body).Decode(&stats); err != nil {
  		t.Fatal(err)
  	}
  	for _, key := range []string{"consumption_rates", "top_used", "top_restocked"} {
  		if _, ok := stats[key]; !ok {
  			t.Errorf("stats missing key %q", key)
  		}
  	}
  	rates := stats["consumption_rates"].([]any)
  	if len(rates) == 0 {
  		t.Error("expected at least one consumption rate entry")
  	}
  }
  ```

- [ ] **Step 2: Run the new tests**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go test ./handlers/... -run "TestGetHistory" -v 2>&1
  ```

  Expected: all 4 PASSes.

- [ ] **Step 3: Run full test suite**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go test ./handlers/... 2>&1
  ```

  Expected: `ok  	kitchen_manager/handlers`.

- [ ] **Step 4: Commit**

  ```bash
  git add handlers/inventory_test.go
  git commit -m "test: add history API endpoint tests"
  ```

---

## Task 7: Frontend — History tab

**Files:**
- Modify: `static/index.html`

This task has no automated tests. Verify by running the server and opening a browser.

- [ ] **Step 1: Add History to the `tabs` array**

  In the `app()` function, find:

  ```js
  { id: 'calendar', label: 'Calendar', icon: '📅' },
  ```

  Add immediately after it (before the closing `]`):

  ```js
  { id: 'history', label: 'History', icon: '📊' },
  ```

- [ ] **Step 2: Add state properties**

  In the `app()` state object, after the `// Calendar` block's last property (`calendarEntryForm`), add:

  ```js
  // History
  historySubTab: 'activity',
  historyRows: [],
  historyTotal: 0,
  historyLimit: 100,
  historyOffset: 0,
  historyStats: null,
  ```

- [ ] **Step 3: Add `fetchHistory` and `fetchHistoryStats` to `switchTab`**

  Find the existing `switchTab` method and add a history branch:

  ```js
  if (tab === 'history') await this.fetchHistory();
  ```

- [ ] **Step 4: Add the seven helper methods**

  Inside the `app()` object (before the closing `};`), add:

  ```js
  async fetchHistory() {
    const res = await fetch(
      `/api/history?limit=${this.historyLimit}&offset=${this.historyOffset}`
    );
    const data = await res.json();
    this.historyRows = data.rows;
    this.historyTotal = data.total;
  },

  async fetchHistoryStats() {
    const res = await fetch('/api/history/stats');
    this.historyStats = await res.json();
  },

  historyDelta(row) {
    if (row.quantity_before == null && row.quantity_after != null) {
      return `+${row.quantity_after} ${row.unit}`;
    }
    if (row.quantity_after == null && row.quantity_before != null) {
      return `-${row.quantity_before} ${row.unit}`;
    }
    if (row.quantity_before == null || row.quantity_after == null) return '—';
    const delta = row.quantity_after - row.quantity_before;
    const sign = delta >= 0 ? '+' : '';
    return `${sign}${Math.round(delta * 1000) / 1000} ${row.unit}`;
  },

  historyTimeAgo(isoString) {
    const diff = Date.now() - new Date(isoString).getTime();
    const minutes = Math.floor(diff / 60000);
    if (minutes < 1) return 'just now';
    if (minutes < 60) return `${minutes}m ago`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours}h ago`;
    const days = Math.floor(hours / 24);
    return `${days}d ago`;
  },

  historyChangeLabel(type) {
    return { create: 'Created', edit: 'Edited', add: 'Added', remove: 'Used', delete: 'Deleted' }[type] || type;
  },

  historyChangeBadgeClass(type) {
    return {
      create: 'bg-green-100 text-green-700',
      add:    'bg-blue-100 text-blue-700',
      remove: 'bg-orange-100 text-orange-700',
      edit:   'bg-gray-100 text-gray-600',
      delete: 'bg-red-100 text-red-700',
    }[type] || 'bg-gray-100 text-gray-600';
  },
  ```

- [ ] **Step 5: Add the History section HTML**

  Find the `</main>` closing tag (after the Calendar section). Insert immediately before `</main>`:

  ```html
  <!-- ===== HISTORY TAB ===== -->
  <section x-show="activeTab === 'history'">

    <!-- Sub-tab toggle -->
    <div class="flex gap-1 my-4 bg-gray-100 rounded-xl p-1">
      <button @click="historySubTab = 'activity'"
              :class="historySubTab === 'activity' ? 'bg-white shadow text-gray-900' : 'text-gray-500'"
              class="flex-1 rounded-lg py-1.5 text-sm font-medium transition-colors">
        Activity
      </button>
      <button @click="historySubTab = 'item-stats'; if (!historyStats) fetchHistoryStats()"
              :class="historySubTab === 'item-stats' ? 'bg-white shadow text-gray-900' : 'text-gray-500'"
              class="flex-1 rounded-lg py-1.5 text-sm font-medium transition-colors">
        Item Stats
      </button>
      <button @click="historySubTab = 'top-items'; if (!historyStats) fetchHistoryStats()"
              :class="historySubTab === 'top-items' ? 'bg-white shadow text-gray-900' : 'text-gray-500'"
              class="flex-1 rounded-lg py-1.5 text-sm font-medium transition-colors">
        Top Items
      </button>
    </div>

    <!-- Activity log -->
    <div x-show="historySubTab === 'activity'">
      <div class="space-y-2">
        <template x-for="row in historyRows" :key="row.id">
          <div class="bg-white rounded-xl shadow-sm border p-3">
            <div class="flex items-center justify-between gap-2">
              <span class="font-medium text-gray-900 truncate flex-1 min-w-0" x-text="row.item_name"></span>
              <span class="text-xs px-1.5 py-0.5 rounded font-medium whitespace-nowrap"
                    :class="historyChangeBadgeClass(row.change_type)"
                    x-text="historyChangeLabel(row.change_type)"></span>
            </div>
            <div class="flex items-center justify-between mt-1 text-sm text-gray-500">
              <span x-text="historyDelta(row)"></span>
              <div class="flex items-center gap-2 text-xs text-gray-400">
                <span x-show="row.changed_by" x-text="row.changed_by"></span>
                <span x-text="historyTimeAgo(row.changed_at)"></span>
              </div>
            </div>
          </div>
        </template>
        <template x-if="historyRows.length === 0">
          <p class="text-center text-gray-400 py-8">No history yet.</p>
        </template>
      </div>
      <div x-show="historyTotal > historyLimit" class="flex justify-between items-center mt-4 text-sm">
        <button @click="historyOffset = Math.max(0, historyOffset - historyLimit); fetchHistory()"
                :disabled="historyOffset === 0"
                class="border rounded-lg px-3 py-1.5 disabled:opacity-40">
          ← Newer
        </button>
        <span class="text-gray-500"
              x-text="`${historyOffset + 1}–${Math.min(historyOffset + historyLimit, historyTotal)} of ${historyTotal}`">
        </span>
        <button @click="historyOffset += historyLimit; fetchHistory()"
                :disabled="historyOffset + historyLimit >= historyTotal"
                class="border rounded-lg px-3 py-1.5 disabled:opacity-40">
          Older →
        </button>
      </div>
    </div>

    <!-- Item stats -->
    <div x-show="historySubTab === 'item-stats'">
      <template x-if="!historyStats">
        <p class="text-center text-gray-400 py-8">Loading...</p>
      </template>
      <template x-if="historyStats">
        <div class="space-y-2">
          <template x-for="item in historyStats.consumption_rates" :key="item.inventory_id">
            <div class="bg-white rounded-xl shadow-sm border p-3 flex items-center justify-between">
              <span class="font-medium text-gray-900 truncate flex-1 min-w-0" x-text="item.item_name"></span>
              <span class="text-sm text-gray-500 ml-2 whitespace-nowrap"
                    x-text="`${item.weekly_rate} ${item.unit}/wk`"></span>
            </div>
          </template>
          <template x-if="historyStats.consumption_rates.length === 0">
            <p class="text-center text-gray-400 py-8">No consumption data in the last 30 days.</p>
          </template>
        </div>
      </template>
    </div>

    <!-- Top items -->
    <div x-show="historySubTab === 'top-items'">
      <template x-if="historyStats">
        <div class="space-y-4">
          <div>
            <h3 class="text-sm font-semibold text-gray-700 mb-2">Top 5 Most Used (last 30 days)</h3>
            <div class="space-y-2">
              <template x-for="(item, i) in historyStats.top_used" :key="item.inventory_id">
                <div class="bg-white rounded-xl shadow-sm border p-3 flex items-center gap-3">
                  <span class="text-sm font-bold text-gray-400 w-5 text-right" x-text="i + 1 + '.'"></span>
                  <span class="font-medium text-gray-900 truncate flex-1 min-w-0" x-text="item.item_name"></span>
                  <span class="text-sm text-orange-600 font-medium whitespace-nowrap"
                        x-text="`${item.total_removed} ${item.unit}`"></span>
                </div>
              </template>
              <template x-if="historyStats.top_used.length === 0">
                <p class="text-center text-gray-400 py-4 text-sm">No data yet.</p>
              </template>
            </div>
          </div>
          <div>
            <h3 class="text-sm font-semibold text-gray-700 mb-2">Top 5 Most Restocked (last 30 days)</h3>
            <div class="space-y-2">
              <template x-for="(item, i) in historyStats.top_restocked" :key="item.inventory_id">
                <div class="bg-white rounded-xl shadow-sm border p-3 flex items-center gap-3">
                  <span class="text-sm font-bold text-gray-400 w-5 text-right" x-text="i + 1 + '.'"></span>
                  <span class="font-medium text-gray-900 truncate flex-1 min-w-0" x-text="item.item_name"></span>
                  <span class="text-sm text-blue-600 font-medium whitespace-nowrap"
                        x-text="`${item.restock_count}×`"></span>
                </div>
              </template>
              <template x-if="historyStats.top_restocked.length === 0">
                <p class="text-center text-gray-400 py-4 text-sm">No data yet.</p>
              </template>
            </div>
          </div>
        </div>
      </template>
      <template x-if="!historyStats">
        <p class="text-center text-gray-400 py-8">Loading...</p>
      </template>
    </div>

  </section>
  ```

- [ ] **Step 6: Update the Tailwind safelist div**

  Find the existing:
  ```html
  <div class="hidden bg-green-600 bg-red-600"></div>
  ```

  Replace with:
  ```html
  <div class="hidden bg-green-600 bg-red-600 bg-green-100 text-green-700 bg-blue-100 text-blue-700 bg-orange-100 text-orange-700 bg-gray-100 text-gray-600 bg-red-100 text-red-700"></div>
  ```

- [ ] **Step 7: Manual smoke test**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go run . &
  ```

  Open `http://localhost:8080` in a browser:
  - Tap Pantry → add an item → edit it → delete it.
  - Tap History tab → Activity sub-tab should show three rows (Created, Edited, Deleted).
  - Tap Item Stats → should show the item's weekly rate (may be 0.00 if under a week).
  - Tap Top Items → top used and top restocked lists (may be empty if no `remove` events).
  - Confirm pagination controls appear correctly when there are more than 100 rows.

  Kill the server after smoke test: `kill %1`

- [ ] **Step 8: Commit**

  ```bash
  git add static/index.html
  git commit -m "feat: add History tab with activity log, item stats, and top items"
  ```

---

## Task 8: Tag grocery scanner commits with `barcode_add` source

**Files:**
- Modify: `static/index.html`

- [ ] **Step 1: Find `commitGroceryItems` in `static/index.html`**

  Locate the `PATCH` call inside `commitGroceryItems()`:

  ```js
  const patch = await fetch(`/api/inventory/${pending.id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ quantity: newQty }),
  });
  ```

- [ ] **Step 2: Append `?source=barcode_add` to the URL**

  Change to:

  ```js
  const patch = await fetch(`/api/inventory/${pending.id}?source=barcode_add`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ quantity: newQty }),
  });
  ```

- [ ] **Step 3: Run full test suite to ensure nothing regressed**

  ```bash
  cd /home/josh/code_projects/kitchen_manager
  go test ./handlers/... 2>&1
  ```

  Expected: `ok  	kitchen_manager/handlers`.

- [ ] **Step 4: Commit**

  ```bash
  git add static/index.html
  git commit -m "feat: tag grocery scanner inventory updates with source=barcode_add"
  ```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|---|---|
| `inventory_history` table with all 10 columns | Task 1 |
| Indexes on `(inventory_id, changed_at)` and `changed_at` | Task 1 |
| Migration safety (`IF NOT EXISTS`) | Task 1 |
| `LogHistory` helper with session email | Task 3 |
| `POST /api/inventory/` logs `create` | Task 5 |
| `PATCH /api/inventory/{id}` logs `edit`/`add`/`remove` | Task 5 |
| `DELETE /api/inventory/{id}` logs `delete` | Task 5 |
| `?source` query param on PATCH | Task 5 |
| `GET /api/history` paginated | Task 3 |
| `GET /api/history/item/{id}` | Task 3 |
| `GET /api/history/stats` with 3 aggregate queries | Task 3 |
| `handlers.SessionManager` set from `main.go` | Task 4 |
| History tab (5th tab) | Task 7 |
| Activity log sub-tab with pagination | Task 7 |
| Item stats sub-tab | Task 7 |
| Top items sub-tab | Task 7 |
| Tailwind safelist | Task 7 |
| Grocery scanner tags `barcode_add` | Task 8 |
| `InventoryHistoryRow` model (reconsidered: lives in handlers as unexported struct) | Task 3 |

All requirements covered. No gaps found.

**Placeholder scan:** No TBD, TODO, or vague steps present.

**Type consistency:** `historyRow` struct defined in Task 3 is used only within `handlers/history.go`. `historyChangeBadgeClass` and `historyChangeLabel` defined in Task 7 Step 4 are called in Task 7 Step 5 HTML — names match.
