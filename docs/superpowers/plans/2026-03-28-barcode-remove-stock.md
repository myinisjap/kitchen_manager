# Barcode Remove Stock Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Remove Stock" barcode scanner to the Inventory tab. Scanning subtracts 1 from a known item's quantity (floor 0), shows a toast, and keeps the scanner open. No pending list, no review step — writes are immediate.

**Architecture:** All changes are frontend-only in `static/index.html`. A new `removeScanOpen` boolean gates a new full-screen modal. The camera machinery (`_stream`, `_codeReader`, `_scanRaf`, `_scannerActive`) is shared with the existing scanners. Four new methods are added: `openRemoveScanner`, `_startRemoveCamera`, `closeRemoveScanner`, `onRemoveBarcodeScanned`.

**Tech Stack:** Alpine.js, ZXing.js / BarcodeDetector API (already loaded), `GET /api/inventory/barcode/{code}`, `PATCH /api/inventory/{id}?source=barcode_remove`.

**Spec:** `docs/superpowers/specs/2026-03-28-barcode-remove-stock-design.md`

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `static/index.html` | Modify | New button, new modal HTML, new Alpine state, new methods |

---

## Task 1: Add Alpine state for remove scanner

**Files:**
- Modify: `static/index.html`

- [ ] **Step 1: Locate the `_scanCooldown` state line**

Find:
```javascript
_scanCooldown: false,
```

- [ ] **Step 2: Add remove-scanner state immediately after that line**

```javascript
removeScanOpen: false,
_removeScanCooldown: false,
removeCameraError: '',
```

- [ ] **Step 3: Verify page loads**

```bash
go run . &
sleep 1
curl -s http://localhost:8080/ | grep -c "removeScanOpen"
pkill -f "go run" 2>/dev/null || true
```

Expected: `1`

- [ ] **Step 4: Commit**

```bash
git -C /home/josh/code_projects/kitchen_manager add static/index.html
git -C /home/josh/code_projects/kitchen_manager commit -m "feat: add remove scanner Alpine state"
```

---

## Task 2: Add "Remove Stock" button to Inventory tab header

**Files:**
- Modify: `static/index.html`

- [ ] **Step 1: Locate the existing Scan button in the Inventory tab header**

Find (around line 36):
```html
<button @click="openScanner('find')" class="border border-gray-300 text-gray-600 px-3 py-2 rounded-lg text-sm font-medium">📷 Scan</button>
```

- [ ] **Step 2: Add the Remove Stock button immediately after the Scan button**

Insert this line between the Scan button and the `+ Add Item` button:

```html
<button @click="openRemoveScanner()" class="bg-red-600 text-white px-3 py-2 rounded-lg text-sm font-medium">📷 Remove Stock</button>
```

- [ ] **Step 3: Verify the button is present**

```bash
cd /home/josh/code_projects/kitchen_manager && go run . &
sleep 1
curl -s http://localhost:8080/ | grep -c "Remove Stock"
pkill -f "go run" 2>/dev/null || true
```

Expected: `1`

- [ ] **Step 4: Commit**

```bash
git -C /home/josh/code_projects/kitchen_manager add static/index.html
git -C /home/josh/code_projects/kitchen_manager commit -m "feat: add Remove Stock button to Inventory tab header"
```

---

## Task 3: Add remove stock scanner modal HTML

**Files:**
- Modify: `static/index.html`

The modal needs: a header with "Remove Stock" label and a "Done" button, a full-height camera view with targeting reticle, and a camera error fallback paragraph. No inline form, no review section.

- [ ] **Step 1: Locate the Grocery Scanner Modal comment**

Find:
```html
  <!-- Grocery Scanner Modal -->
```

- [ ] **Step 2: Insert the Remove Stock modal immediately before that comment**

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

- [ ] **Step 3: Verify the modal element is in the page**

```bash
cd /home/josh/code_projects/kitchen_manager && go run . &
sleep 1
curl -s http://localhost:8080/ | grep -c "remove-scanner-video"
pkill -f "go run" 2>/dev/null || true
```

Expected: `1`

- [ ] **Step 4: Commit**

```bash
git -C /home/josh/code_projects/kitchen_manager add static/index.html
git -C /home/josh/code_projects/kitchen_manager commit -m "feat: add Remove Stock scanner modal HTML"
```

---

## Task 4: Add remove scanner methods

**Files:**
- Modify: `static/index.html`

Add all four methods: `openRemoveScanner`, `_startRemoveCamera`, `closeRemoveScanner`, `onRemoveBarcodeScanned`. The insertion point is immediately after the closing `},` of `closeGroceryScanner()`, keeping all scanner methods grouped together.

- [ ] **Step 1: Locate the `closeGroceryScanner` method**

Find:
```javascript
        closeGroceryScanner() {
```

Locate its closing `},` (the method ends with the line `        },` after the `this._stream` cleanup).

- [ ] **Step 2: Insert the four methods immediately after `closeGroceryScanner`'s closing `},`**

```javascript
        async openRemoveScanner() {
          if (this._scannerActive) this.closeScanner();
          this.removeScanOpen = true;
          this._removeScanCooldown = false;
          this.removeCameraError = '';
          await this.$nextTick();
          await this._startRemoveCamera();
        },

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

        closeRemoveScanner() {
          this.removeScanOpen = false;
          this._removeScanCooldown = false;
          this.removeCameraError = '';
          this._scannerActive = false;
          if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
          if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
          if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
        },

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

- [ ] **Step 3: Verify the methods are present**

```bash
cd /home/josh/code_projects/kitchen_manager && go run . &
sleep 1
curl -s http://localhost:8080/ | grep -c "openRemoveScanner"
curl -s http://localhost:8080/ | grep -c "onRemoveBarcodeScanned"
pkill -f "go run" 2>/dev/null || true
```

Expected: both `1`

- [ ] **Step 4: Commit**

```bash
git -C /home/josh/code_projects/kitchen_manager add static/index.html
git -C /home/josh/code_projects/kitchen_manager commit -m "feat: remove scanner methods — open/start/close/onScan"
```

---

## Task 5: Smoke test end-to-end

**Files:** none (verification only)

- [ ] **Step 1: Start the server**

```bash
cd /home/josh/code_projects/kitchen_manager && go run . &
sleep 2
```

- [ ] **Step 2: Verify all injected identifiers are present**

```bash
curl -s http://localhost:8080/ | grep -c "Remove Stock"
curl -s http://localhost:8080/ | grep -c "remove-scanner-video"
curl -s http://localhost:8080/ | grep -c "removeScanOpen"
curl -s http://localhost:8080/ | grep -c "_removeScanCooldown"
curl -s http://localhost:8080/ | grep -c "removeCameraError"
curl -s http://localhost:8080/ | grep -c "openRemoveScanner"
curl -s http://localhost:8080/ | grep -c "onRemoveBarcodeScanned"
```

Expected: each line outputs `1`.

- [ ] **Step 3: Verify the Tailwind red safelist class covers the button**

```bash
curl -s http://localhost:8080/ | grep -c "bg-red-600"
```

Expected: at least `2` (the Tailwind safelist `<div class="hidden bg-green-600 bg-red-600">` already contains `bg-red-600`, plus the new button).

- [ ] **Step 4: Stop the server**

```bash
pkill -f "go run" 2>/dev/null || true
```

---

## Task 6: Manual browser test checklist

Perform in a browser on a device with a camera (or Chrome DevTools camera emulation).

- [ ] Open Inventory (Pantry) tab — confirm "📷 Remove Stock" button is visible, red, positioned between the Scan button and "+ Add Item".
- [ ] Tap "Remove Stock" — confirm full-screen black modal opens, camera activates, "Remove Stock" header and "Done" button are visible.
- [ ] Tap "Done" — confirm modal closes, camera light goes off.
- [ ] Reopen scanner — scan a known barcode — confirm green toast `"{name} -1 {unit}"`.
- [ ] Scan the same barcode within 2.5 s — confirm it is silently ignored (no second toast fires).
- [ ] Wait 3 s and scan again — confirm a new toast appears.
- [ ] Scan an unknown barcode — confirm red toast "Item not found".
- [ ] Find an item at quantity 0 in inventory; scan it — confirm quantity stays at 0, not -1 (toast still shows `-1`).
- [ ] Verify the inventory list refreshes after a successful scan (items visible in background behind modal update when modal is closed).
- [ ] Deny camera permission (or block in DevTools) — confirm error message appears in the modal instead of a camera view.
- [ ] Verify the grocery scanner (Shopping tab "Scan Groceries") still opens and works normally after opening and closing the Remove Stock scanner.
