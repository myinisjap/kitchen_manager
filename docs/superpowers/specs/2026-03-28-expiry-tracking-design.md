# Expiry Tracking Design

**Date:** 2026-03-28

## Goal

Surface expiration information visually inside the Inventory tab so the user can see at a glance which items are expired or expiring within 7 days. No push notifications or background jobs are needed — everything is driven by page load and tab-switch fetches that already exist.

---

## What Does NOT Change

- The `inventory` table schema — `expiration_date` is already a nullable `TEXT` column in `YYYY-MM-DD` format.
- `GET /api/inventory/` — returns all items unchanged; no expiry filtering is added to this endpoint.
- `POST /api/inventory/` and `PATCH /api/inventory/{id}` — expiry date handling is already in place.
- The existing `expiringItems` state variable name and `fetchExpiring()` method name.
- All other tabs (Shopping, Recipes, Calendar).
- Toast notifications — no new toasts are introduced by this feature.
- Any backend endpoint other than `GET /api/inventory/expiring`.

---

## API Changes

### Extend `GET /api/inventory/expiring`

**Current behaviour:** returns items where `expiration_date >= today AND expiration_date <= today+days`. Rejects `days < 0`.

**New behaviour:** also return items that are already expired (i.e. `expiration_date < today`). The single endpoint is extended rather than adding a separate `/expired` route — one fetch gives the UI everything it needs for both the collapsible section and the per-item badges.

Remove the `days < 0` rejection. The query becomes:

```sql
SELECT id, name, quantity, unit, location, expiration_date, low_threshold, barcode, preferred_unit
FROM inventory
WHERE expiration_date != ''
  AND expiration_date <= ?
```

The single bind parameter is `cutoff = time.Now().AddDate(0, 0, days).Format("2006-01-02")`.

When `days=7` (the default and the only value the UI ever sends), this returns:
- Items expiring today through 7 days from now (expiry >= today is no longer filtered, but they all satisfy `<= cutoff`).
- Items already expired (expiry < today also satisfies `<= cutoff`).

The `days` parameter still defaults to `7` when absent and still validates that the provided string is a valid integer. The `days < 0` error response is removed; negative values are accepted (they would return only already-expired items expiring more than `|days|` days ago, but the UI never sends a negative value).

**Response shape:** unchanged — array of inventory item objects, each with the standard fields including `expiration_date`.

**No new endpoint is added.** `GET /api/inventory/expired` is not needed.

---

## Backend Changes (`handlers/inventory.go`)

Inside the `GET /api/inventory/expiring` handler, make two targeted changes:

1. Remove the guard `if days < 0 { WriteError(...); return }`.
2. Replace the two-parameter date-range query with a single upper-bound query:

```go
cutoff := time.Now().AddDate(0, 0, days).Format("2006-01-02")
rows, err := db.Query(
    `SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit
     FROM inventory
     WHERE expiration_date != '' AND expiration_date <= ?`,
    cutoff,
)
```

The `today` variable and the `>= ?` bind are deleted. Everything else in the handler (JSON encoding via `scanInventoryRows`, error handling, `WriteJSON`) is unchanged.

---

## Alpine.js State Additions

Add one new state property alongside the existing `expiringItems: []`:

```js
expiringSectionOpen: true,
```

`expiringSectionOpen` controls whether the collapsible "Expiring Soon" section is expanded. It defaults to `true` so the user sees the section immediately on first load.

No other state properties change. `expiringItems` continues to hold the array returned by `fetchExpiring()`, which now includes already-expired items.

---

## Frontend: `fetchExpiring()` Change

The function body is unchanged — it still fetches `/api/inventory/expiring?days=7`. Because the backend now includes expired items in that response, no URL change is needed.

---

## Frontend: Helper Function

Add a pure helper method inside the `app()` object. It is called per item in the inventory list template to derive badge state from `item.expiration_date`:

```js
expiryBadge(item) {
  if (!item.expiration_date) return null;
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const exp = new Date(item.expiration_date + 'T00:00:00');
  const diffMs = exp - today;
  const diffDays = Math.round(diffMs / 86400000);
  if (diffDays < 0) return { label: 'Expired', style: 'red' };
  if (diffDays === 0) return { label: 'Expires today', style: 'amber' };
  if (diffDays <= 7) return { label: `Expires in ${diffDays} day${diffDays === 1 ? '' : 's'}`, style: 'amber' };
  return null;
},
```

Returns `null` when no badge should be shown (no expiry date, or expiry > 7 days away).

---

## UI Changes

### 1. Expiring Soon collapsible section

Replace the existing static banner (currently the `<template x-if="expiringItems.length > 0">` block at lines 41–45 of `static/index.html`) with the following. The new block goes in the same position — between the search/filter controls and the item list, inside the `<section x-show="activeTab === 'inventory'">` section.

```html
<!-- Expiring Soon section -->
<template x-if="expiringItems.length > 0">
  <div class="mb-4">
    <!-- Collapsible header -->
    <button
      @click="expiringSectionOpen = !expiringSectionOpen"
      class="w-full flex items-center justify-between bg-amber-50 border border-amber-200 rounded-lg px-3 py-2 text-sm font-medium text-amber-800">
      <span>
        ⚠️ Expiring Soon
        <span class="ml-1 bg-amber-200 text-amber-900 text-xs font-semibold px-1.5 py-0.5 rounded-full"
              x-text="expiringItems.length"></span>
      </span>
      <span x-text="expiringSectionOpen ? '▲' : '▼'" class="text-amber-600 text-xs"></span>
    </button>

    <!-- Collapsible body -->
    <div x-show="expiringSectionOpen"
         x-transition:enter="transition ease-out duration-150"
         x-transition:enter-start="opacity-0 -translate-y-1"
         x-transition:enter-end="opacity-100 translate-y-0"
         x-transition:leave="transition ease-in duration-100"
         x-transition:leave-start="opacity-100 translate-y-0"
         x-transition:leave-end="opacity-0 -translate-y-1">
      <div class="border border-amber-200 border-t-0 rounded-b-lg bg-white divide-y divide-gray-100">
        <template x-for="item in expiringItems" :key="item.id">
          <div class="flex items-center justify-between px-3 py-2 text-sm">
            <span class="font-medium text-gray-900 truncate flex-1 min-w-0" x-text="item.name"></span>
            <span class="text-gray-500 mx-3 whitespace-nowrap">
              <span x-text="item.quantity"></span> <span x-text="item.unit"></span>
            </span>
            <span class="text-gray-400 text-xs mx-2 whitespace-nowrap" x-text="item.expiration_date"></span>
            <span :class="expiryBadge(item)?.style === 'red'
                            ? 'bg-red-100 text-red-700'
                            : 'bg-amber-100 text-amber-700'"
                  class="text-xs px-1.5 py-0.5 rounded whitespace-nowrap"
                  x-text="expiryBadge(item)?.label"></span>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>
```

Each row in the section shows: item name | quantity + unit | expiration_date string | badge label.

### 2. Per-item expiry badge in the inventory list

Inside the existing `<template x-for="item in inventoryItems">` card, inside the `flex items-center gap-2` div that already contains the item name and the "Low" badge, add immediately after the Low badge span:

```html
<template x-if="expiryBadge(item)">
  <span :class="expiryBadge(item).style === 'red'
                  ? 'bg-red-100 text-red-700'
                  : 'bg-amber-100 text-amber-700'"
        class="text-xs px-1.5 py-0.5 rounded whitespace-nowrap"
        x-text="expiryBadge(item).label"></span>
</template>
```

The existing `&bull; Exp: <span x-text="item.expiration_date">` sub-line is kept. The badge is an additive, at-a-glance signal in the name row.

### 3. Tailwind safelist extension

The safelist comment at the bottom of the HTML file (currently `<div class="hidden bg-green-600 bg-red-600">`) must include the new dynamic classes so they are not purged by Tailwind:

```html
<div class="hidden bg-green-600 bg-red-600 bg-amber-50 bg-amber-100 bg-amber-200 text-amber-700 text-amber-800 text-amber-900 bg-red-100 text-red-700"></div>
```

---

## Data Flow

1. On app `init()` and on every switch to the Inventory tab, `fetchExpiring()` fires alongside `fetchInventory()`.
2. `fetchExpiring()` calls `GET /api/inventory/expiring?days=7`.
3. The Go handler queries SQLite for all items with a non-empty `expiration_date <= today+7days`; this now includes already-expired items.
4. The response is stored in `expiringItems`.
5. If `expiringItems.length > 0`, the Expiring Soon section renders. The header shows the count. Each row calls `expiryBadge(item)` to determine label and colour.
6. `fetchInventory()` independently populates `inventoryItems` (unchanged). Each card calls `expiryBadge(item)` inline — the helper reads `item.expiration_date` directly and does not consult `expiringItems`.

---

## Edge Cases

- **Item with no expiration_date:** `expiryBadge()` returns `null`; no badge renders anywhere.
- **Item expiring exactly today:** `diffDays === 0`; label is "Expires today", amber.
- **Item expired yesterday:** `diffDays === -1`; label is "Expired", red.
- **`expiration_date` is an empty string from the DB:** empty string is falsy; the `!item.expiration_date` guard catches it.
- **Timezone:** both `new Date()` (client local, zeroed to midnight) and `new Date(item.expiration_date + 'T00:00:00')` (parsed as local midnight) use the client's local timezone consistently, avoiding UTC date-shift off-by-one errors.
- **Same item in both lists:** an expiring item appears in `expiringItems` (summary section) and in `inventoryItems` (main list card). Both show the badge independently via `expiryBadge()`.
- **Section collapse persists within the session:** `expiringSectionOpen` is not reset on tab switch or list refresh, so if the user collapses the section it stays collapsed until they expand it again.

---

## What Is Not Implemented

- Push notifications or background polling.
- Automatic deletion or archiving of expired items.
- A dedicated `GET /api/inventory/expired` endpoint.
- Any changes to the Shopping, Recipes, or Calendar tabs.
- Any change to how `expiration_date` is stored or edited in the Add/Edit Item modal.
- Sorting the Expiring Soon rows by days remaining (rows appear in DB insertion order, which is acceptable for an MVP).
