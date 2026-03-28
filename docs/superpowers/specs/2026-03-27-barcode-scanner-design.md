# Barcode Scanner Design

**Date:** 2026-03-27
**Status:** Approved

## Overview

Add barcode scanning to the inventory system. Two entry points: a "Scan" button on the inventory tab that finds an existing item by barcode or pre-fills a new one, and a scan icon next to the barcode field in the Add/Edit modal that fills the field in place.

Uses ZXing-js (CDN) for client-side camera-based decoding. Unknown barcodes are looked up against Open Food Facts (free, no API key) to pre-fill the item name. One new backend endpoint looks up an item by its barcode value.

## Backend — `GET /api/inventory/barcode/{code}`

New endpoint in `handlers/inventory.go`, registered before `GET /api/inventory/{id}`:

```
GET /api/inventory/barcode/{code}
```

- Exact match on the `barcode` column
- Returns the full inventory item JSON (same shape as other inventory endpoints) on match
- Returns 404 with `{"error": "not found"}` if no item has that barcode
- Empty `code` is not routable (Go ServeMux won't match it)

New test `TestGetInventoryItemByBarcode` in `handlers/inventory_test.go`:
- Insert item with a known barcode, fetch by that barcode → 200 + correct item
- Fetch with unknown barcode → 404

## Frontend — ZXing-js

Loaded from CDN (unpkg or jsDelivr), `@zxing/library` UMD build. No npm/build step.

```html
<script src="https://unpkg.com/@zxing/library@0.19.1/umd/index.min.js"></script>
```

### Scanner modal

Full-screen overlay (`z-50`) with:
- `<video id="scanner-video">` for the live viewfinder
- A "Cancel" button
- A status line ("Point at a barcode…")
- Brief green flash on successful decode

Rear camera requested via `facingMode: "environment"`.

ZXing decoding runs via `codeReader.decodeOnceFromVideoDevice(undefined, 'scanner-video')` — ZXing handles the camera stream lifecycle internally. On close/cancel, `codeReader.reset()` is called to stop the stream and release the camera.

### Alpine.js state additions

```js
scannerOpen: false,
scannerMode: '',        // 'find' | 'fill'
codeReader: null,
```

### Two scanner entry points

**1. Inventory tab "Scan" button** (`scannerMode: 'find'`)

On decode:
1. Call `GET /api/inventory/barcode/{code}`
2. **Found** → close scanner, call `editItem(foundItem)` (same method invoked by clicking edit on an inventory card)
3. **Not found** → fetch `https://world.openfoodfacts.org/api/v2/product/{code}.json`
   - If product found → open Add Item modal with `barcode`, `name` (from `product.product_name`) pre-filled
   - If not found or fetch fails → open Add Item modal with only `barcode` pre-filled

**2. Barcode field scan icon in Add/Edit modal** (`scannerMode: 'fill'`)

On decode: set `itemForm.barcode = code`, close scanner. No lookup performed.

### Barcode field in Add/Edit modal

Added to `itemForm`:
```js
barcode: '',
```

Displayed below the expiration date field:
```html
<div class="flex gap-2 items-center">
  <input x-model="itemForm.barcode" placeholder="Barcode" class="flex-1 border rounded-lg px-3 py-2 text-sm" />
  <button @click="openScanner('fill')" ...><!-- camera icon --></button>
</div>
```

### Error handling

| Scenario | Behaviour |
|----------|-----------|
| Camera permission denied | Toast "Camera access denied — enter barcode manually", close scanner |
| Open Food Facts no result | Open Add Item with barcode pre-filled only (silent) |
| Open Food Facts network error | Same silent fallback |
| Barcode not in inventory, no external lookup result | Open Add Item with barcode pre-filled only |

## Testing

### Backend
- `TestGetInventoryItemByBarcode` — match returns 200 + item; no match returns 404

### Frontend (manual, on device)
- Open app on mobile browser, tap "Scan", grant camera → viewfinder appears
- Point at grocery barcode → item found or Add modal opens pre-filled
- In Add modal, tap scan icon next to barcode field → fills barcode field only
- Deny camera permission → toast appears, no crash
- Tap Cancel during scan → scanner closes, no item opened

## Tech Stack

- Go 1.22+ stdlib `net/http`, `modernc.org/sqlite`
- ZXing-js `@zxing/library@0.19.1` (CDN UMD build)
- Open Food Facts API (client-side fetch, no API key)
- Alpine.js (CDN, existing)
