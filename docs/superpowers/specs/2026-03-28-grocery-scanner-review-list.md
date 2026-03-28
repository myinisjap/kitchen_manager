# Grocery Scanner Review List Design

**Date:** 2026-03-28

## Goal

Replace the instant-PATCH-on-scan flow with a scan-accumulate-then-review flow. Scans build a pending list; the user reviews and commits all at once after tapping "Done". Also throttle the scan rate so the pill has time to display before the next scan is accepted.

---

## Scan Rate Throttle

After a successful decode (barcode recognized, item found or not), block new scans for 2.5 seconds. Implemented via a `_scanCooldown` boolean flag set to `true` on scan and cleared after 2.5s via `setTimeout`. The guard in `onGroceryBarcodeScanned` checks `_scanCooldown` alongside the existing guards.

---

## Pending List

### Data structure

`groceryPendingItems` — array of objects:
```js
{ id, name, qty, unit, barcode }
```
- `id` — inventory item ID (for the PATCH)
- `name` — display name
- `qty` — accumulated quantity (starts at OFf-parsed qty or 1, incremented on repeat scans)
- `unit` — unit string
- `barcode` — for deduplication lookup

Same barcode scanned again: find existing entry by `barcode`, increment `qty` by the same per-scan amount (1 or OFf-parsed qty).

### OFf lookup

After adding an item to the pending list with qty=1, OFf lookup runs in background (3s timeout). If it resolves a quantity, update the pending list entry's `qty` and `unit` in place (replace the initial 1, don't add to it).

### Unknown items

If barcode not in inventory (404), the existing unknown-item form flow applies — the user fills in the form and taps "Add to Inventory". This still POSTs immediately (no change to unknown-item flow). Unknown items do NOT go into `groceryPendingItems`.

---

## Modal States

The modal has two views, toggled by `groceryScanReview: false`.

### Scan view (groceryScanReview === false) — unchanged camera UI

- Camera feed + crosshair overlay
- Green/red pill toasts as before (show item name and qty added to pending list)
- Badge changes from "N scanned" to "N pending" showing `groceryPendingItems.length`
- "Done" button calls `finishGroceryScan()`

### Review view (groceryScanReview === true) — replaces camera area

Header changes to "Review Scanned Items".

List of pending items:
```
Eggs        +2 piece    [✕]
Whole Milk  +1 L        [✕]
```
Each row has a remove button (✕) that splices the item from `groceryPendingItems`.

If list is empty (all removed): show "No items to update" message.

Bottom action bar:
- **"Update Inventory"** button (green) — calls `commitGroceryItems()`
- **"Cancel"** button (gray) — discards list, closes modal

### Transitions

- "Done" with items pending → switch to review view (stop camera)
- "Done" with no items pending → close modal immediately (existing behavior)
- "Update Inventory" → PATCH all items, close modal on success
- "Cancel" → close modal, discard pending list

---

## New / Changed Methods

### `onGroceryBarcodeScanned(code)` — changed

1. Guard: `if (!this.groceryScanOpen || this.groceryScanPaused || this._groceryProcessing || this._scanCooldown) return;`
2. Set `this._scanCooldown = true; setTimeout(() => this._scanCooldown = false, 2500);`
3. Fetch `/api/inventory/barcode/{code}`
4. If 200 (known item):
   - Check if `groceryPendingItems` already has an entry with this barcode
   - If yes: increment `qty` by 1 (OFf will refine later if needed)
   - If no: push `{ id: item.id, name: item.name, qty: 1, unit: item.unit, barcode: code }`
   - Show success toast: `"{name} +1 {unit}"`
   - Run OFf lookup in background (3s timeout); if resolved, update the matching pending entry's `qty` and `unit` and update the toast
   - Do NOT PATCH
5. If 404: existing unknown-item form flow (unchanged)
6. Reset `_groceryProcessing = false` after inventory lookup resolves

### `finishGroceryScan()` — new

Called by "Done" button.
```js
finishGroceryScan() {
  if (this.groceryPendingItems.length === 0) {
    this.closeGroceryScanner();
    return;
  }
  // Stop camera, switch to review view
  this._scannerActive = false;
  if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
  if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
  if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
  this.groceryScanReview = true;
}
```

### `commitGroceryItems()` — new

Iterates `groceryPendingItems`, fetches current inventory quantity for each item, then PATCHes with `currentQty + pendingQty`. Shows a single toast on completion.

```js
async commitGroceryItems() {
  for (const pending of this.groceryPendingItems) {
    try {
      // Fetch current quantity (may have changed since scan)
      const res = await fetch(`/api/inventory/${pending.id}`);  // GET by id — use barcode endpoint or list
      // Actually: re-fetch via barcode to get latest quantity
      const cur = await fetch('/api/inventory/barcode/' + encodeURIComponent(pending.barcode));
      if (!cur.ok) continue;
      const item = await cur.json();
      const newQty = parseFloat((item.quantity + pending.qty).toFixed(4));
      await fetch(`/api/inventory/${pending.id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ quantity: newQty }),
      });
    } catch { /* skip failed items */ }
  }
  this.fetchInventory();
  this.closeGroceryScanner();
  this.showToast(`Updated ${this.groceryPendingItems.length} item(s)`);
}
```

Note: `GET /api/inventory/{id}` does not exist as a dedicated endpoint — use `GET /api/inventory/barcode/{barcode}` to re-fetch current quantity.

### `closeGroceryScanner()` — changed

Add reset of new state fields:
```js
this.groceryPendingItems = [];
this.groceryScanReview = false;
this._scanCooldown = false;
```

### `openGroceryScanner()` — changed

Add reset of new state fields alongside existing resets:
```js
this.groceryPendingItems = [];
this.groceryScanReview = false;
this._scanCooldown = false;
```

---

## State Additions

```js
groceryPendingItems: [],
groceryScanReview: false,
_scanCooldown: false,
```

Remove `groceryScanCount` — replaced by `groceryPendingItems.length` for the badge.

---

## Modal HTML Changes

- Badge: change from `groceryScanCount + ' scanned'` to `groceryPendingItems.length + ' pending'`
- "Done" button: change `@click` from `closeGroceryScanner()` to `finishGroceryScan()`
- Camera section: wrap in `<div x-show="!groceryScanReview">`
- Add review section: `<div x-show="groceryScanReview">` containing the list and action buttons

---

## What Does NOT Change

- Unknown-item form flow (404 path) — still POSTs immediately
- All other toasts outside the grocery scanner
- Backend / Go code
- Inventory tab scanner
