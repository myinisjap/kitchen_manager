# Shopping Barcode Scanner Design

**Date:** 2026-03-28

## Goal

Add a "Scan Groceries" button to the Shopping tab that lets the user scan barcodes to update inventory quantities (for known items) or add new inventory entries (for unknown items). Distinct from the Inventory tab scanner — this is a restock flow, not an edit flow.

---

## User Flow

1. User taps **"Scan Groceries"** button in the Shopping tab header
2. A modal opens with a live camera view (same ZXing.js scanner used in Inventory tab)
3. For each barcode scanned:
   - **Known item (barcode in inventory):** increment quantity on the existing entry, show toast ("Eggs +12"), scanner stays open
   - **Unknown item (not in inventory, found on Open Food Facts):** pause scanner, show a pre-filled add-item form (name, quantity, unit, location from OFf + previous inventory history). User confirms → item added to inventory → scanner resumes
   - **Unknown item (not in OFf either):** pause scanner, show a blank add-item form with only barcode pre-filled. User fills in manually → scanner resumes
   - **Scan error / unreadable:** show brief toast ("Couldn't read barcode"), scanner stays open
4. User taps **"Done"** to close the modal and return to the shopping list

The scanner stays open between scans. The Inventory tab scanner is unchanged.

---

## UI Changes (index.html only)

### Shopping tab header
Add a **"Scan Groceries"** button alongside the existing "+ Add" button.

### Scan Groceries modal
- Full-width camera `<video>` element (same as inventory scanner)
- "Done" button to close
- Item count badge showing how many items have been scanned this session (e.g. "3 scanned")
- When paused for new-item confirmation: show the add-item form inline (same fields as inventory add form), with a "Add to Inventory" confirm button and a "Skip" button to discard and resume scanning

---

## Logic

### Barcode resolution (same as existing inventory scanner)

1. Call `GET /api/inventory/barcode/{code}`
2. If 200 → known item path
3. If 404 → call Open Food Facts `GET https://world.openfoodfacts.org/api/v2/product/{code}.json`
4. If OFf has data → pre-fill new item form
5. If OFf has no data → show empty form with barcode pre-filled

### Known item: quantity increment

- Fetch current item quantity and units from the barcode lookup response
- Determine scanned quantity:
  - If OFf returns `product_quantity` + `product_quantity_unit`, use those
  - Otherwise default to `+1` in the item's existing unit
- Unit conversion: if scanned unit and item's `preferred_unit` are the same dimension, convert before adding. If different dimension or unrecognized unit, default to `+1` in the item's existing unit (no conversion attempted)
- Call `PATCH /api/inventory/{id}` with `{ quantity: newQuantity }`
- Show toast: `"{name} +{amount} {unit}"`
- Refresh inventory in background (no visible list update needed in Shopping tab)

### Unknown item: location pre-fill

When showing the add-item form for an unknown barcode, after name is determined (from OFf or user input), query existing inventory for any item with the same name and pre-fill `location` from the most recent match. This reuses the existing suggestions endpoint: `GET /api/inventory/suggestions?name={name}` already returns name+unit pairs; location can be fetched from the first inventory match.

Actually: the suggestions endpoint does not return location. Instead, after OFf lookup resolves a name, call `GET /api/inventory/?name={name}` and take the `location` from the first result if any. This is a frontend-only lookup, no new API needed.

### Duplicate scan guard

Same barcode scanned twice in the same session: treat as two separate increments. No deduplication.

---

## What Does NOT Change

- Inventory tab barcode scanner
- All API handlers (no new backend endpoints needed)
- Shopping list itself — this feature only touches inventory, not the shopping list items
- The existing add-item form component — reused as-is for the unknown item path

---

## Error Handling

| Scenario | Behaviour |
|----------|-----------|
| Camera permission denied | Show error message in modal, "Done" button to close |
| Barcode unreadable | Toast "Couldn't read barcode", scanner stays open |
| OFf fetch fails | Silent fallback to blank form with barcode pre-filled |
| PATCH /api/inventory fails | Toast "Failed to update {name}", scanner stays open |
| Unit dimension mismatch | Default to +1 in item's existing unit, toast shows "+1 {unit}" |
