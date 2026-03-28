# Inventory History and Statistics Design

**Date:** 2026-03-28

## Goal

Record every change to inventory items in a permanent audit log and expose that log — plus aggregate consumption statistics — through three new API endpoints and a new History tab in the frontend. The feature answers two questions: "what happened to my pantry?" (activity log) and "what do I use most?" (stats and top items).

---

## What Does NOT Change

- The `inventory` table schema and all its existing columns.
- Any existing `GET`, `PATCH`, `POST`, or `DELETE` endpoint signatures or response shapes for inventory, shopping, recipes, or calendar.
- The existing tab order for Pantry, Shopping, Recipes, and Calendar tabs — the History tab is inserted between Calendar and nothing (it becomes the fifth tab).
- The `scanInventoryRow` / `scanInventoryRows` helpers.
- The `WriteJSON` / `ReadJSON` / `WriteError` helpers in `handlers/helpers.go`.
- The session/OAuth machinery in `auth.go` and `main.go` — history logging calls the same `sessionManager.GetString` pattern already used in `authMiddleware`.
- The `newTestDB` helper in `handlers/inventory_test.go` — it will need the `inventory_history` table added, but its shape and usage pattern are unchanged.
- All existing tests — none are deleted or modified.

---

## Schema

### New table: `inventory_history`

Add to `createSchema()` in `db.go` inside the existing `db.Exec` call alongside the other `CREATE TABLE IF NOT EXISTS` statements:

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

**Column semantics:**

| Column | Notes |
|---|---|
| `inventory_id` | Foreign key by convention only (no `REFERENCES` constraint so rows survive item deletion) |
| `item_name` | Snapshot of `name` at the time of the change — survives item deletion |
| `changed_at` | UTC timestamp, set by SQLite default |
| `changed_by` | Email string from session; `NULL` when OAuth is disabled or session has no email |
| `change_type` | One of: `create`, `edit`, `add`, `remove`, `delete` |
| `quantity_before` | `NULL` for `create` events; actual quantity for all others |
| `quantity_after` | `NULL` for `delete` events; actual quantity for all others |
| `unit` | Unit at the time of the change (snapshot) |
| `source` | One of: `manual`, `barcode_add`, `barcode_remove`, `threshold`, `recipe`; `NULL` if not applicable |

**`change_type` rules:**

- `create` — item row inserted via `POST /api/inventory/`
- `delete` — item row removed via `DELETE /api/inventory/{id}`
- `edit` — `PATCH` request that changes any field other than (or in addition to) quantity
- `add` — `PATCH` request that changes only `quantity` and the new value is greater than the old value
- `remove` — `PATCH` request that changes only `quantity` and the new value is less than or equal to the old value

### Migration

The `preferred_unit` migration pattern already in `db.go` is the model. After the main `db.Exec` call, add a column-existence check for `inventory_history`:

```go
// Migration: create inventory_history if it does not exist
// (databases created before this feature lack the table)
_, err = db.Exec(`
    CREATE TABLE IF NOT EXISTS inventory_history ( ... );
    CREATE INDEX IF NOT EXISTS inventory_history_item_idx ...;
    CREATE INDEX IF NOT EXISTS inventory_history_changed_at_idx ...;
`)
```

Because `CREATE TABLE IF NOT EXISTS` is idempotent, this is safe to run on every startup against both old and new databases.

---

## New Model

Add to `models.go`:

```go
// InventoryHistoryRow is one entry in the inventory audit log.
type InventoryHistoryRow struct {
    ID             int64    `json:"id"`
    InventoryID    int64    `json:"inventory_id"`
    ItemName       string   `json:"item_name"`
    ChangedAt      string   `json:"changed_at"`   // RFC3339 string from SQLite
    ChangedBy      *string  `json:"changed_by"`   // nil when no auth
    ChangeType     string   `json:"change_type"`
    QuantityBefore *float64 `json:"quantity_before"`
    QuantityAfter  *float64 `json:"quantity_after"`
    Unit           string   `json:"unit"`
    Source         *string  `json:"source"`
}
```

---

## Backend

### New file: `handlers/history.go`

This file owns all history-related logic: the `LogHistory` helper, `RegisterHistory`, and the three handler functions.

#### `LogHistory` helper

```go
func LogHistory(db *sql.DB, r *http.Request, inventoryID int64, itemName string,
    changeType string, quantityBefore, quantityAfter *float64,
    unit, source string) error
```

- Reads `changed_by` from the session if `sessionManager != nil`:
  `email := sessionManager.GetString(r.Context(), "email")` — this requires `sessionManager` to be accessible from the handlers package (see Wiring section below).
- Inserts one row into `inventory_history`.
- Returns an error but the callers log it and continue — a history write failure must not cause the main operation to fail.

`sessionManager` is currently a package-level var in `package main`. To make it accessible from `package handlers`, pass it as a parameter to `RegisterHistory` (and thus store it in a closure-accessible variable within the file), or expose it via a setter. The cleanest approach consistent with the existing pattern (where `db` is passed to every Register function) is to pass `sessionManager` as an `interface{ GetString(context.Context, string) string }` to `LogHistory` directly, with `nil` acceptable when OAuth is disabled.

In practice: add a package-level `var SessionManager interface{ GetString(context.Context, string) string }` in `handlers/history.go`, and set it from `main.go` after `sessionManager` is created:

```go
// in main.go, inside the OAUTH_ENABLED block, after sessionManager = newSessionManager()
handlers.SessionManager = sessionManager
```

`LogHistory` then does:

```go
var changedBy *string
if handlers.SessionManager != nil {
    email := handlers.SessionManager.GetString(r.Context(), "email")
    if email != "" {
        changedBy = &email
    }
}
```

#### `RegisterHistory(mux *http.ServeMux, db *sql.DB)`

Registers three routes:

1. `GET /api/history`
2. `GET /api/history/item/{id}`
3. `GET /api/history/stats`

---

### Modifying existing handlers to call `LogHistory`

#### `POST /api/inventory/` in `handlers/inventory.go`

After the successful `db.Exec` INSERT and `id, _ := res.LastInsertId()`:

```go
qAfter := item.Quantity
if err := LogHistory(db, r, id, item.Name, "create", nil, &qAfter, item.Unit, "manual"); err != nil {
    // log but do not fail
}
```

The `source` is always `"manual"` here because there is no way to pass a source through this endpoint without a breaking schema change. The barcode grocery scanner that calls `POST /api/inventory/` via the frontend should be treated as `"manual"` at the DB level; barcode-sourced quantity increments arrive via `PATCH` and can be tagged `barcode_add` through a query parameter (see below).

#### `PATCH /api/inventory/{id}` in `handlers/inventory.go`

Before applying patches, fetch the current row:

```go
row := db.QueryRow(`SELECT id,name,quantity,unit FROM inventory WHERE id=?`, id)
var curID int64
var curName, curUnit string
var curQty float64
if err := row.Scan(&curID, &curName, &curQty, &curUnit); err != nil {
    // item not found will be caught below; skip history
}
```

After all field updates succeed and the updated item is fetched:

```go
newQty := item["quantity"].(float64)   // item is the result of scanInventoryRow after update
source := r.URL.Query().Get("source")  // optional ?source=barcode_add etc.
if source == "" { source = "manual" }

// Determine change_type
onlyQtyChanged := /* only "quantity" key was in patch */
var changeType string
if onlyQtyChanged {
    if newQty > curQty { changeType = "add" } else { changeType = "remove" }
} else {
    changeType = "edit"
}
LogHistory(db, r, id, curName, changeType, &curQty, &newQty, curUnit, source)
```

To detect `onlyQtyChanged`: check `len(patch) == 1 && patch["quantity"] != nil`.

The frontend commits grocery-scanner additions via `PATCH` with no `?source` parameter today. After this change, the frontend should append `?source=barcode_add` when calling from `commitGroceryItems()`. The spec for barcode-remove (future feature) will similarly append `?source=barcode_remove`.

#### `DELETE /api/inventory/{id}` in `handlers/inventory.go`

Before the `db.Exec` DELETE, fetch the item's name, quantity, and unit:

```go
row := db.QueryRow(`SELECT name,quantity,unit FROM inventory WHERE id=?`, id)
var name, unit string
var qty float64
row.Scan(&name, &qty, &unit)
```

After confirming `n > 0`:

```go
LogHistory(db, r, id, name, "delete", &qty, nil, unit, "manual")
```

---

## New API Endpoints

### `GET /api/history`

**Purpose:** Paginated full history log, newest first.

**Query parameters:**

| Parameter | Default | Description |
|---|---|---|
| `limit` | `100` | Maximum rows to return (capped at 500) |
| `offset` | `0` | Number of rows to skip |

**SQL:**

```sql
SELECT id, inventory_id, item_name, changed_at, changed_by,
       change_type, quantity_before, quantity_after, unit, source
FROM inventory_history
ORDER BY changed_at DESC, id DESC
LIMIT ? OFFSET ?
```

**Response:** `200 OK`

```json
{
  "rows": [
    {
      "id": 42,
      "inventory_id": 7,
      "item_name": "Milk",
      "changed_at": "2026-03-28T14:22:00Z",
      "changed_by": "alice@example.com",
      "change_type": "remove",
      "quantity_before": 2.0,
      "quantity_after": 1.5,
      "unit": "L",
      "source": "manual"
    }
  ],
  "total": 142,
  "limit": 100,
  "offset": 0
}
```

`total` is a separate `SELECT COUNT(*)` executed in the same handler before the paged query.

**Error responses:**

- `400 Bad Request` — `limit` or `offset` is not a valid non-negative integer: `{"error": "invalid limit"}` / `{"error": "invalid offset"}`

---

### `GET /api/history/item/{id}`

**Purpose:** Full history for one inventory item.

**Path parameter:** `id` — integer inventory item ID.

**Query parameters:** none (returns all rows for the item, newest first, no pagination needed for MVP).

**SQL:**

```sql
SELECT id, inventory_id, item_name, changed_at, changed_by,
       change_type, quantity_before, quantity_after, unit, source
FROM inventory_history
WHERE inventory_id = ?
ORDER BY changed_at DESC, id DESC
```

**Response:** `200 OK`

```json
[
  {
    "id": 42,
    "inventory_id": 7,
    "item_name": "Milk",
    "changed_at": "2026-03-28T14:22:00Z",
    "changed_by": "alice@example.com",
    "change_type": "remove",
    "quantity_before": 2.0,
    "quantity_after": 1.5,
    "unit": "L",
    "source": "manual"
  }
]
```

Returns `[]` (empty array, not null) when no history exists for the item.

**Error responses:**

- `400 Bad Request` — `id` is not a valid integer: `{"error": "invalid id"}`

---

### `GET /api/history/stats`

**Purpose:** Aggregate consumption statistics over the last 30 days.

**Query parameters:** none.

**Computation:**

The handler runs three SQL queries and assembles a single JSON object.

**Query 1 — per-item weekly consumption rate:**

A "removal" event is any row with `change_type IN ('remove', 'delete')`. The weekly rate is `SUM(quantity_before - quantity_after) / (30.0 / 7.0)` over the last 30 days.

```sql
SELECT inventory_id, item_name, unit,
       SUM(quantity_before - quantity_after) / (30.0 / 7.0) AS weekly_rate
FROM inventory_history
WHERE change_type IN ('remove', 'delete')
  AND changed_at >= datetime('now', '-30 days')
  AND quantity_before IS NOT NULL
  AND quantity_after IS NOT NULL
GROUP BY inventory_id, item_name, unit
ORDER BY weekly_rate DESC
```

Note: `delete` events have `quantity_after = NULL`, so they are excluded from this computation unless the handler explicitly handles them. For simplicity, `delete` rows are excluded from the consumption rate because `quantity_after` is NULL — the WHERE clause `quantity_after IS NOT NULL` handles this cleanly.

**Query 2 — top 5 most-used items (by total quantity removed):**

```sql
SELECT inventory_id, item_name, unit,
       SUM(quantity_before - quantity_after) AS total_removed
FROM inventory_history
WHERE change_type = 'remove'
  AND changed_at >= datetime('now', '-30 days')
  AND quantity_before IS NOT NULL
  AND quantity_after IS NOT NULL
GROUP BY inventory_id, item_name, unit
ORDER BY total_removed DESC
LIMIT 5
```

**Query 3 — top 5 most restocked items (by number of `add` or `create` events):**

```sql
SELECT inventory_id, item_name,
       COUNT(*) AS restock_count
FROM inventory_history
WHERE change_type IN ('add', 'create')
  AND changed_at >= datetime('now', '-30 days')
GROUP BY inventory_id, item_name
ORDER BY restock_count DESC
LIMIT 5
```

**Response:** `200 OK`

```json
{
  "consumption_rates": [
    {
      "inventory_id": 7,
      "item_name": "Milk",
      "unit": "L",
      "weekly_rate": 1.75
    }
  ],
  "top_used": [
    {
      "inventory_id": 7,
      "item_name": "Milk",
      "unit": "L",
      "total_removed": 7.5
    }
  ],
  "top_restocked": [
    {
      "inventory_id": 7,
      "item_name": "Milk",
      "restock_count": 4
    }
  ]
}
```

All three arrays return `[]` (not null) when empty. `weekly_rate` and `total_removed` are rounded to 2 decimal places in the Go handler before serialisation using `math.Round(v*100) / 100`.

---

## Wiring in `main.go`

`RegisterHistory` is called alongside the other Register calls:

```go
handlers.RegisterHistory(mux, db)
```

The `handlers.SessionManager` assignment goes inside the `OAUTH_ENABLED` block immediately after `sessionManager = newSessionManager()`:

```go
sessionManager = newSessionManager()
handlers.SessionManager = sessionManager
```

---

## Alpine.js State Additions

Add to the `app()` state object alongside the existing properties:

```js
// History tab
historySubTab: 'activity',   // 'activity' | 'item-stats' | 'top-items'
historyRows: [],
historyTotal: 0,
historyLimit: 100,
historyOffset: 0,
historyStats: null,          // null until first fetch; then { consumption_rates, top_used, top_restocked }
```

Add `'history'` to the `tabs` array between `calendar` and nothing (it becomes the last tab):

```js
{ id: 'history', label: 'History', icon: '📊' },
```

Add to `switchTab`:

```js
if (tab === 'history') await this.fetchHistory();
```

### New methods

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
  // Returns a display string like "+1.5 L" or "-0.25 kg"
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

`fetchHistoryStats()` is called when the user selects the "Item stats" or "Top items" sub-tab, lazily (only if `historyStats` is null or when the sub-tab is activated).

---

## Frontend: History Tab UI

The History tab section is inserted in `static/index.html` immediately after the Calendar section and before `</main>`. It follows the same `<section x-show="activeTab === 'history'">` pattern used by all other tabs.

### Tab bar

The tab bar `<template x-for="tab in tabs">` already renders dynamically — adding the entry to the `tabs` array is sufficient. No HTML change to the tab bar template is needed.

### History section structure

```html
<!-- ===== HISTORY TAB ===== -->
<section x-show="activeTab === 'history'">

  <!-- Sub-tab toggle -->
  <div class="flex gap-1 my-4 bg-gray-100 rounded-xl p-1">
    <button @click="historySubTab = 'activity'"
            :class="historySubTab === 'activity'
              ? 'bg-white shadow text-gray-900'
              : 'text-gray-500'"
            class="flex-1 rounded-lg py-1.5 text-sm font-medium transition-colors">
      Activity
    </button>
    <button @click="historySubTab = 'item-stats'; if (!historyStats) fetchHistoryStats()"
            :class="historySubTab === 'item-stats'
              ? 'bg-white shadow text-gray-900'
              : 'text-gray-500'"
            class="flex-1 rounded-lg py-1.5 text-sm font-medium transition-colors">
      Item Stats
    </button>
    <button @click="historySubTab = 'top-items'; if (!historyStats) fetchHistoryStats()"
            :class="historySubTab === 'top-items'
              ? 'bg-white shadow text-gray-900'
              : 'text-gray-500'"
            class="flex-1 rounded-lg py-1.5 text-sm font-medium transition-colors">
      Top Items
    </button>
  </div>

  <!-- ---- Activity log ---- -->
  <div x-show="historySubTab === 'activity'">
    <div class="space-y-2">
      <template x-for="row in historyRows" :key="row.id">
        <div class="bg-white rounded-xl shadow-sm border p-3">
          <div class="flex items-center justify-between gap-2">
            <span class="font-medium text-gray-900 truncate flex-1 min-w-0"
                  x-text="row.item_name"></span>
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

    <!-- Pagination -->
    <div x-show="historyTotal > historyLimit"
         class="flex justify-between items-center mt-4 text-sm">
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

  <!-- ---- Item stats ---- -->
  <div x-show="historySubTab === 'item-stats'">
    <template x-if="!historyStats">
      <p class="text-center text-gray-400 py-8">Loading...</p>
    </template>
    <template x-if="historyStats">
      <div class="space-y-2">
        <template x-for="item in historyStats.consumption_rates" :key="item.inventory_id">
          <div class="bg-white rounded-xl shadow-sm border p-3 flex items-center justify-between">
            <span class="font-medium text-gray-900 truncate flex-1 min-w-0"
                  x-text="item.item_name"></span>
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

  <!-- ---- Top items ---- -->
  <div x-show="historySubTab === 'top-items'">
    <template x-if="historyStats">
      <div class="space-y-4">

        <div>
          <h3 class="text-sm font-semibold text-gray-700 mb-2">Top 5 Most Used (last 30 days)</h3>
          <div class="space-y-2">
            <template x-for="(item, i) in historyStats.top_used" :key="item.inventory_id">
              <div class="bg-white rounded-xl shadow-sm border p-3 flex items-center gap-3">
                <span class="text-sm font-bold text-gray-400 w-5 text-right"
                      x-text="i + 1 + '.'"></span>
                <span class="font-medium text-gray-900 truncate flex-1 min-w-0"
                      x-text="item.item_name"></span>
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
                <span class="text-sm font-bold text-gray-400 w-5 text-right"
                      x-text="i + 1 + '.'"></span>
                <span class="font-medium text-gray-900 truncate flex-1 min-w-0"
                      x-text="item.item_name"></span>
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

### Tailwind safelist extension

Extend the existing safelist div at the bottom of `static/index.html`:

```html
<div class="hidden bg-green-600 bg-red-600 bg-green-100 text-green-700 bg-blue-100 text-blue-700 bg-orange-100 text-orange-700 bg-gray-100 text-gray-600 bg-red-100 text-red-700"></div>
```

---

## Data Flow

### On write (any inventory mutation)

1. Frontend calls `POST /api/inventory/`, `PATCH /api/inventory/{id}`, or `DELETE /api/inventory/{id}`.
2. Go handler validates and executes the inventory mutation as today.
3. Handler calls `LogHistory(...)` with the appropriate `change_type`, before/after quantities, and source.
4. `LogHistory` reads the email from the session (if OAuth is enabled) and inserts one row into `inventory_history`.
5. Response is returned to the client unchanged.

### On read (History tab)

1. User taps the History tab → `switchTab('history')` → `fetchHistory()`.
2. `GET /api/history?limit=100&offset=0` returns the 100 most recent rows.
3. Rows are rendered in the Activity log view.
4. When the user taps "Item Stats" or "Top Items" sub-tab and `historyStats` is null, `fetchHistoryStats()` fires.
5. `GET /api/history/stats` runs three aggregate queries and returns the assembled object.
6. Both sub-tabs read from `historyStats`.

---

## Edge Cases

- **Item deleted, then history viewed:** `item_name` snapshot in `inventory_history` preserves the name. `inventory_id` will point to a deleted row but that is acceptable — history rows are permanent.
- **OAuth disabled (`OAUTH_ENABLED` != `"true"`):** `handlers.SessionManager` is never set, so it is `nil`. `LogHistory` checks for nil and writes `changed_by = NULL`.
- **`PATCH` with empty body or no valid fields:** The existing handler silently no-ops. `LogHistory` is still called, but `change_type` will be `"edit"` with identical before/after quantities — this is acceptable noise.
- **Concurrent writes (SQLite single-writer):** `db.SetMaxOpenConns(1)` is already set. History inserts are serialised alongside inventory writes with no additional locking needed.
- **Stats with no data:** All three queries return zero rows → three empty arrays → valid JSON response with `[]`.
- **`quantity_before - quantity_after` negative in consumption query:** This should not occur because the WHERE clause filters to `change_type IN ('remove', 'delete')`, but if it does (e.g. a `remove` event where quantity went up due to a race), the row contributes a negative value to the sum. This is acceptable for an MVP — no clamping is applied.
- **Pagination past end:** `offset >= total` returns `{"rows": [], "total": N, "limit": 100, "offset": M}`. The frontend disables the "Older" button via `:disabled="historyOffset + historyLimit >= historyTotal"`.

---

## What Is Not Implemented

- Per-item history drill-down from the Inventory tab (the `GET /api/history/item/{id}` endpoint is wired but no UI entry point is added — this is reserved for a future detail view).
- Filtering or searching the activity log by item, user, source, or date range.
- Export (CSV, JSON download) of the history log.
- Barcode-remove source tagging (the `?source=barcode_remove` convention is documented here but the barcode-remove feature itself is a separate spec).
- Automatic pruning or archiving of old history rows.
- Any change to the shopping, recipes, or calendar handlers — they do not touch inventory quantities directly.
