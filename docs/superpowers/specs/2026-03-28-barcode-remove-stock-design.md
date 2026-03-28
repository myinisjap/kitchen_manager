# Barcode Remove Stock Design

**Date:** 2026-03-28

## Goal

Add a "Remove Stock" scanner to the Inventory tab. Scanning a barcode immediately subtracts 1 from the matched item's quantity (floor at 0) and shows a toast. This is the complement to the grocery (restock) scanner: that flow adds stock, this flow removes it. No pending list, no review step — each scan is committed immediately.

---

## What Does NOT Change

- The existing "📷 Scan" button in the Inventory tab header (barcode lookup / item-fill flow).
- The grocery scanner modal, state, and methods (`groceryScanOpen`, `_startGroceryCamera`, `onGroceryBarcodeScanned`, etc.).
- All backend handlers — no new endpoints are introduced.
- The inventory item edit modal and all other modals.
- The `scannerOpen` / `scannerMode` / `onBarcodeScanned` inventory-lookup scanner.
- The shared scanner hardware state (`_stream`, `_codeReader`, `_scanRaf`, `_scannerActive`) — only one scanner runs at a time; these are reused.

---

## UI Changes (`static/index.html` only)

### Inventory tab header

Current buttons (left to right): search input, location select, `📷 Scan`, `+ Add Item`.

After this change: search input, location select, `📷 Scan`, **`📷 Remove Stock`** (red), `+ Add Item`.

Exact new button:
```html
<button @click="openRemoveScanner()" class="bg-red-600 text-white px-3 py-2 rounded-lg text-sm font-medium">📷 Remove Stock</button>
```

Place it immediately after the existing `📷 Scan` button and before the `+ Add Item` button.

### Remove Stock scanner modal

A new full-screen modal, parallel in structure to the grocery scanner modal. It uses `removeCameraError` for its own camera-error state.

```html
<!-- Remove Stock Scanner Modal -->
<div x-show="removeScanOpen" class="fixed inset-0 bg-black z-50 flex flex-col">
  <!-- Header -->
  <div class="flex items-center justify-between p-4">
    <span class="text-white text-sm font-medium">Remove Stock</span>
    <button @click="closeRemoveScanner()" class="text-white text-sm border border-white/40 rounded-lg px-3 py-1.5">Done</button>
  </div>

  <!-- Camera -->
  <div class="relative overflow-hidden flex-1">
    <video id="remove-scanner-video" class="absolute inset-0 w-full h-full object-cover" playsinline x-show="!removeCameraError"></video>
    <div x-show="removeCameraError" class="absolute inset-0 flex items-center justify-center bg-black">
      <p class="text-red-400 text-sm text-center px-6" x-text="removeCameraError"></p>
    </div>
    <div x-show="!removeCameraError" class="absolute inset-0 flex items-center justify-center pointer-events-none">
      <div class="w-64 h-40 border-2 border-white/70 rounded-lg"></div>
    </div>
  </div>
</div>
```

Place this modal immediately before the `<!-- Grocery Scanner Modal -->` comment.

The modal is intentionally minimal — no inline forms, no pending list, no pause state. The camera fills the screen except for the header.

---

## Alpine State Additions

Add these three fields to the Alpine `app()` data object, immediately after the `_scanCooldown: false,` line:

```javascript
removeScanOpen: false,
_removeScanCooldown: false,
removeCameraError: '',
```

- `removeScanOpen` — controls modal visibility.
- `_removeScanCooldown` — set to `true` for 2.5 s after each successful scan decode; prevents double-scans of the same barcode within the cooldown window.
- `removeCameraError` — error string shown in the modal when camera access fails.

---

## New Alpine Methods

### `openRemoveScanner()`

Opens the modal, resets state, starts the camera.

```javascript
async openRemoveScanner() {
  if (this._scannerActive) this.closeScanner();
  this.removeScanOpen = true;
  this._removeScanCooldown = false;
  this.removeCameraError = '';
  await this.$nextTick();
  await this._startRemoveCamera();
},
```

### `_startRemoveCamera()`

Mirrors `_startGroceryCamera()` exactly, except:
- Guards on `removeScanOpen` instead of `groceryScanOpen`.
- The video element ID is `remove-scanner-video`.
- Sets `removeCameraError` instead of `groceryCameraError`.
- Calls `this.onRemoveBarcodeScanned(code)` on a successful decode.
- The cooldown check is inside `onRemoveBarcodeScanned` via `_removeScanCooldown`.
- There is no pause state — the BarcodeDetector loop only checks `_scannerActive`.

```javascript
async _startRemoveCamera() {
  if (!this.removeScanOpen) return;
  const video = document.getElementById('remove-scanner-video');
  try {
    const stream = await navigator.mediaDevices.getUserMedia({
      video: { facingMode: { ideal: 'environment' }, width: { ideal: 1920 }, height: { ideal: 1080 } }
    });
    if (!this.removeScanOpen) { stream.getTracks().forEach(t => t.stop()); return; }
    this._stream = stream;
    video.srcObject = stream;
    await new Promise(resolve => { video.onloadedmetadata = resolve; });
    video.play();
  } catch {
    this.removeCameraError = 'Camera access denied. Please allow camera access and try again.';
    return;
  }

  this._scannerActive = true;
  let lastScanErrorToast = 0;

  if (typeof BarcodeDetector !== 'undefined') {
    const detector = new BarcodeDetector({ formats: ['ean_13','ean_8','upc_a','upc_e','code_128','code_39','qr_code','itf','codabar'] });
    const scan = async () => {
      if (!this._scannerActive) return;
      try {
        const codes = await detector.detect(video);
        if (codes.length > 0) { lastScanErrorToast = 0; this.onRemoveBarcodeScanned(codes[0].rawValue); }
      } catch {
        const now = Date.now();
        if (now - lastScanErrorToast > 2000) {
          lastScanErrorToast = now;
          this.showToast("Couldn't read barcode", 'error');
        }
      }
      this._scanRaf = requestAnimationFrame(scan);
    };
    this._scanRaf = requestAnimationFrame(scan);
  } else {
    this._codeReader = new ZXing.BrowserMultiFormatReader();
    this._codeReader.decodeFromVideoElementContinuously(video, (result, err) => {
      if (result) { lastScanErrorToast = 0; this.onRemoveBarcodeScanned(result.getText()); return; }
      if (err && !(err instanceof ZXing.NotFoundException)) {
        const now = Date.now();
        if (now - lastScanErrorToast > 2000) {
          lastScanErrorToast = now;
          this.showToast("Couldn't read barcode", 'error');
        }
      }
    });
  }
},
```

### `closeRemoveScanner()`

Stops the camera, tears down scanner state, closes modal.

```javascript
closeRemoveScanner() {
  this.removeScanOpen = false;
  this._removeScanCooldown = false;
  this.removeCameraError = '';
  this._scannerActive = false;
  if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
  if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
  if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
},
```

### `onRemoveBarcodeScanned(code)`

Called for every successfully decoded barcode. Applies a 2.5 s cooldown, fetches the item by barcode, decrements quantity by 1 (floor 0), PATCHes with `?source=barcode_remove`, shows a toast.

```javascript
async onRemoveBarcodeScanned(code) {
  if (!this.removeScanOpen || this._removeScanCooldown) return;
  this._removeScanCooldown = true;
  setTimeout(() => { this._removeScanCooldown = false; }, 2500);

  try {
    const res = await fetch('/api/inventory/barcode/' + encodeURIComponent(code));
    if (!res.ok) {
      this.showToast('Item not found', 'error');
      return;
    }
    const item = await res.json();
    const newQty = Math.max(0, item.quantity - 1);
    const patch = await fetch(`/api/inventory/${item.id}?source=barcode_remove`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ quantity: newQty }),
    });
    if (patch.ok) {
      this.showToast(`${item.name} -1 ${item.unit}`, 'success');
      this.fetchInventory();
    } else {
      this.showToast(`Failed to update ${item.name}`, 'error');
    }
  } catch {
    this.showToast('Failed to update item', 'error');
  }
},
```

---

## API Usage

| Method | URL | Purpose |
|--------|-----|---------|
| `GET` | `/api/inventory/barcode/{code}` | Look up item by scanned barcode |
| `PATCH` | `/api/inventory/{id}?source=barcode_remove` | Set quantity to `max(0, current - 1)` |

No new backend endpoints are added. The `?source=barcode_remove` query parameter is consumed by the inventory history feature (see `2026-03-28-inventory-history-design.md`) and is silently ignored by the current handler if history logging is not yet deployed.

### PATCH body

```json
{ "quantity": <max(0, item.quantity - 1)> }
```

The floor of 0 is enforced on the frontend with `Math.max(0, item.quantity - 1)`. The backend independently rejects negative quantities, so this is double-guarded.

---

## Scan Behaviour

| Event | Behaviour |
|-------|-----------|
| Known barcode, PATCH succeeds | Toast `"{name} -1 {unit}"` (green), scanner stays open |
| Known barcode, PATCH fails (non-2xx) | Toast `"Failed to update {name}"` (red), scanner stays open |
| Unknown barcode (barcode lookup returns non-200) | Toast `"Item not found"` (red), scanner stays open |
| Barcode decode error (camera frame unreadable) | Toast `"Couldn't read barcode"` (red), scanner stays open; rate-limited to once per 2 s |
| Camera access denied | `removeCameraError` set; error shown in modal; video hidden |
| Same barcode scanned within 2.5 s cooldown | Silently ignored |
| Item quantity already 0 | PATCH sets it to 0 (no change), toast still shows `"{name} -1 {unit}"` |

The scanner never pauses, never shows a form, and never requires confirmation. Every write is immediate.

---

## Error Handling

| Scenario | Frontend behaviour |
|----------|--------------------|
| `getUserMedia` throws | `removeCameraError` set; video hidden; error paragraph shown in modal |
| `GET /api/inventory/barcode/{code}` returns 404 or other non-200 | Toast "Item not found" (red) |
| `GET /api/inventory/barcode/{code}` network error | Toast "Failed to update item" (red) |
| `PATCH /api/inventory/{id}` non-2xx | Toast "Failed to update {name}" (red) |
| `PATCH /api/inventory/{id}` network error | Toast "Failed to update item" (red) |
| BarcodeDetector `.detect()` throws | Toast "Couldn't read barcode" (red), rate-limited to once per 2 s |
| ZXing non-`NotFoundException` error | Toast "Couldn't read barcode" (red), rate-limited to once per 2 s |

---

## Mutual Exclusion With Other Scanners

All three scanners (inventory lookup, grocery restock, remove stock) share `_stream`, `_codeReader`, `_scanRaf`, and `_scannerActive`. `openRemoveScanner()` calls `this.closeScanner()` if `_scannerActive` is true, tearing down whichever inventory-lookup scanner was running. This matches the pattern used by `openGroceryScanner()`. Only one scanner runs at a time.
