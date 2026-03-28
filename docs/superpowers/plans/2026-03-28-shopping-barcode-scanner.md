# Shopping Barcode Scanner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Scan Groceries" button to the Shopping tab that increments inventory quantities for known barcodes and shows a pre-filled add form for unknown ones, keeping the scanner open between scans.

**Architecture:** All changes are frontend-only in `static/index.html`. A new scanner mode `'restock'` is added to the existing `openScanner(mode)` / `onBarcodeScanned(code)` pattern. A new `groceryScanModal` boolean controls a separate modal that wraps the camera alongside an inline new-item form. The existing scanner modal (`scannerOpen`) is unchanged.

**Tech Stack:** Alpine.js, ZXing.js / BarcodeDetector API (already loaded), existing REST API endpoints (`GET /api/inventory/barcode/{code}`, `PATCH /api/inventory/{id}`, `POST /api/inventory/`, Open Food Facts API)

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `static/index.html` | Modify | All changes — new button, new modal, new Alpine state, new methods |

---

## Task 1: Add Alpine state for grocery scanner

**Files:**
- Modify: `static/index.html`

The existing Alpine data object (starting around line 455) needs new state for the grocery scanner session.

- [ ] **Step 1: Locate the Alpine data init block**

Find the line that reads:
```javascript
scannerOpen: false,
```

- [ ] **Step 2: Add grocery scanner state after `_scanRaf: null,`**

After the line `_scanRaf: null,`, add:

```javascript
groceryScanOpen: false,
groceryScanCount: 0,
groceryScanPaused: false,
groceryNewItem: null, // prefill object when paused for unknown item
groceryItemForm: { name: '', quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1, barcode: '' },
```

- [ ] **Step 3: Verify the page still loads**

```bash
# Start server if not running
go run . &
sleep 1
curl -s http://localhost:8080/ | grep -c "Alpine"
```

Expected: `1` (Alpine script tag found — page loads without syntax errors).

- [ ] **Step 4: Commit**

```bash
git add static/index.html
git commit -m "feat: add grocery scanner Alpine state"
```

---

## Task 2: Add "Scan Groceries" button to Shopping tab

**Files:**
- Modify: `static/index.html`

- [ ] **Step 1: Locate the Shopping tab header buttons**

Find this block (around line 80):
```html
<button @click="openAddShoppingItem()" class="bg-green-600 text-white rounded-lg px-3 py-2 text-sm font-medium">+ Add</button>
```

- [ ] **Step 2: Add Scan Groceries button before the + Add button**

Replace:
```html
        <button @click="openAddShoppingItem()" class="bg-green-600 text-white rounded-lg px-3 py-2 text-sm font-medium">+ Add</button>
```

With:
```html
        <button @click="openGroceryScanner()" class="bg-purple-600 text-white rounded-lg px-3 py-2 text-sm font-medium">📷 Scan Groceries</button>
        <button @click="openAddShoppingItem()" class="bg-green-600 text-white rounded-lg px-3 py-2 text-sm font-medium">+ Add</button>
```

- [ ] **Step 3: Verify the button appears**

```bash
curl -s http://localhost:8080/ | grep -c "Scan Groceries"
```

Expected: `1`

- [ ] **Step 4: Commit**

```bash
git add static/index.html
git commit -m "feat: add Scan Groceries button to shopping tab"
```

---

## Task 3: Add grocery scanner modal HTML

**Files:**
- Modify: `static/index.html`

The modal needs: camera view, scan count badge, Done button, and an inline new-item form that appears when scanner is paused for an unknown item.

- [ ] **Step 1: Locate the existing scanner modal**

Find this comment:
```html
  <!-- Scanner Modal -->
```

- [ ] **Step 2: Add the grocery scanner modal immediately before the existing Scanner Modal comment**

```html
  <!-- Grocery Scanner Modal -->
  <div x-show="groceryScanOpen" class="fixed inset-0 bg-black z-50 flex flex-col">
    <!-- Header -->
    <div class="flex items-center justify-between p-4">
      <div class="flex items-center gap-3">
        <span class="text-white text-sm font-medium">Scan Groceries</span>
        <span x-show="groceryScanCount > 0"
              class="bg-white/20 text-white text-xs px-2 py-0.5 rounded-full"
              x-text="groceryScanCount + ' scanned'"></span>
      </div>
      <button @click="closeGroceryScanner()" class="text-white text-sm border border-white/40 rounded-lg px-3 py-1.5">Done</button>
    </div>

    <!-- Camera -->
    <div class="relative overflow-hidden" :class="groceryScanPaused ? 'h-48' : 'flex-1'">
      <video id="grocery-scanner-video" class="absolute inset-0 w-full h-full object-cover" playsinline></video>
      <div x-show="!groceryScanPaused" class="absolute inset-0 flex items-center justify-center pointer-events-none">
        <div class="w-64 h-40 border-2 border-white/70 rounded-lg"></div>
      </div>
      <div x-show="groceryScanPaused" class="absolute inset-0 bg-black/60 flex items-center justify-center">
        <span class="text-white text-sm">Scanner paused</span>
      </div>
    </div>

    <!-- New item form (shown when paused for unknown barcode) -->
    <div x-show="groceryScanPaused && groceryNewItem !== null" class="flex-1 overflow-y-auto bg-white p-4">
      <h3 class="font-semibold text-gray-800 mb-3">New Item — Add to Inventory</h3>
      <div class="space-y-3">
        <div>
          <label class="block text-xs text-gray-500 mb-1">Name</label>
          <input x-model="groceryItemForm.name" type="text" placeholder="Item name"
                 class="w-full border rounded-lg px-3 py-2 text-sm" />
        </div>
        <div class="flex gap-2">
          <div class="flex-1">
            <label class="block text-xs text-gray-500 mb-1">Quantity</label>
            <input x-model.number="groceryItemForm.quantity" type="number" min="0" step="any"
                   class="w-full border rounded-lg px-3 py-2 text-sm" />
          </div>
          <div class="flex-1">
            <label class="block text-xs text-gray-500 mb-1">Unit</label>
            <select x-model="groceryItemForm.unit" class="w-full border rounded-lg px-3 py-2 text-sm">
              <option value="">—</option>
              <template x-for="u in allUnits" :key="u"><option :value="u" x-text="u"></option></template>
            </select>
          </div>
        </div>
        <div>
          <label class="block text-xs text-gray-500 mb-1">Location</label>
          <input x-model="groceryItemForm.location" type="text" placeholder="e.g. Pantry, Fridge"
                 class="w-full border rounded-lg px-3 py-2 text-sm" />
        </div>
        <div>
          <label class="block text-xs text-gray-500 mb-1">Expiry (optional)</label>
          <input x-model="groceryItemForm.expiration_date" type="date"
                 class="w-full border rounded-lg px-3 py-2 text-sm" />
        </div>
      </div>
      <div class="flex gap-2 mt-4">
        <button @click="confirmGroceryNewItem()"
                class="flex-1 bg-green-600 text-white rounded-lg px-4 py-2 text-sm font-medium">
          Add to Inventory
        </button>
        <button @click="skipGroceryNewItem()"
                class="flex-1 border border-gray-300 text-gray-600 rounded-lg px-4 py-2 text-sm font-medium">
          Skip
        </button>
      </div>
    </div>
  </div>

```

- [ ] **Step 3: Verify page loads**

```bash
curl -s http://localhost:8080/ | grep -c "grocery-scanner-video"
```

Expected: `1`

- [ ] **Step 4: Commit**

```bash
git add static/index.html
git commit -m "feat: add grocery scanner modal HTML"
```

---

## Task 4: Add the OFf unit parsing helper

**Files:**
- Modify: `static/index.html`

The OFf unit parsing logic exists inside `onBarcodeScanned` but we need it as a reusable helper to avoid duplicating ~30 lines of code.

- [ ] **Step 1: Locate the offUnitMap block inside onBarcodeScanned**

Find this comment inside `onBarcodeScanned`:
```javascript
          // Map Open Food Facts unit strings to our valid units
          const offUnitMap = {
```

- [ ] **Step 2: Extract the OFf lookup into a standalone helper method**

Find the method `showToast(msg)` and add a new method `parseOffQuantity(product)` immediately before it:

```javascript
        parseOffQuantity(product) {
          const offUnitMap = {
            'g': 'g', 'gram': 'g', 'grams': 'g',
            'kg': 'kg', 'kilogram': 'kg', 'kilograms': 'kg',
            'oz': 'oz', 'ounce': 'oz', 'ounces': 'oz',
            'lb': 'lb', 'lbs': 'lb', 'pound': 'lb', 'pounds': 'lb',
            'ml': 'ml', 'milliliter': 'ml', 'milliliters': 'ml', 'millilitre': 'ml',
            'l': 'L', 'L': 'L', 'liter': 'L', 'liters': 'L', 'litre': 'L', 'litres': 'L',
            'cl': 'ml',
          };
          let qty = null, unit = null;
          if (product.product_quantity && product.product_quantity_unit) {
            const rawUnit = product.product_quantity_unit.trim().toLowerCase();
            const mapped = offUnitMap[rawUnit] || offUnitMap[product.product_quantity_unit.trim()];
            if (mapped) {
              qty = parseFloat(product.product_quantity);
              unit = mapped;
              if (rawUnit === 'cl') qty = qty * 10;
            }
          }
          if ((qty === null || unit === null) && product.quantity) {
            const m = product.quantity.trim().match(/^([\d.,]+)\s*([a-zA-Z]+)/);
            if (m) {
              const rawUnit = m[2].trim().toLowerCase();
              const mapped = offUnitMap[rawUnit] || offUnitMap[m[2].trim()];
              if (mapped) {
                qty = parseFloat(m[1].replace(',', '.'));
                unit = mapped;
                if (rawUnit === 'cl') qty = qty * 10;
              }
            }
          }
          return (qty !== null && unit !== null && !isNaN(qty)) ? { qty, unit } : null;
        },

```

- [ ] **Step 3: Replace the inline OFf unit parsing in onBarcodeScanned with a call to parseOffQuantity**

Find this block inside `onBarcodeScanned` (after `if (p.product_name) prefill.name = p.product_name;`):

```javascript
                // Map Open Food Facts unit strings to our valid units
                const offUnitMap = {
                  'g': 'g', 'gram': 'g', 'grams': 'g',
                  'kg': 'kg', 'kilogram': 'kg', 'kilograms': 'kg',
                  'oz': 'oz', 'ounce': 'oz', 'ounces': 'oz',
                  'lb': 'lb', 'lbs': 'lb', 'pound': 'lb', 'pounds': 'lb',
                  'ml': 'ml', 'milliliter': 'ml', 'milliliters': 'ml', 'millilitre': 'ml',
                  'l': 'L', 'L': 'L', 'liter': 'L', 'liters': 'L', 'litre': 'L', 'litres': 'L',
                  'cl': 'ml', // centiliters → convert to ml below
                };

                let qty = null, unit = null;

                // Prefer structured fields
                if (p.product_quantity && p.product_quantity_unit) {
                  const rawUnit = p.product_quantity_unit.trim().toLowerCase();
                  const mapped = offUnitMap[rawUnit] || offUnitMap[p.product_quantity_unit.trim()];
                  if (mapped) {
                    qty = parseFloat(p.product_quantity);
                    unit = mapped;
                    if (rawUnit === 'cl') qty = qty * 10; // cl → ml
                  }
                }

                // Fallback: parse freeform quantity string e.g. "500 g", "1.5 L"
                if ((qty === null || unit === null) && p.quantity) {
                  const m = p.quantity.trim().match(/^([\d.,]+)\s*([a-zA-Z]+)/);
                  if (m) {
                    const rawUnit = m[2].trim().toLowerCase();
                    const mapped = offUnitMap[rawUnit] || offUnitMap[m[2].trim()];
                    if (mapped) {
                      qty = parseFloat(m[1].replace(',', '.'));
                      unit = mapped;
                      if (rawUnit === 'cl') qty = qty * 10;
                    }
                  }
                }

                if (qty !== null && unit !== null && !isNaN(qty)) {
                  prefill.quantity = qty;
                  prefill.unit = unit;
                  prefill.preferred_unit = unit;
                }
```

Replace with:

```javascript
                const parsed = this.parseOffQuantity(p);
                if (parsed) {
                  prefill.quantity = parsed.qty;
                  prefill.unit = parsed.unit;
                  prefill.preferred_unit = parsed.unit;
                }
```

- [ ] **Step 4: Verify page loads and existing scanner still works**

```bash
curl -s http://localhost:8080/ | grep -c "parseOffQuantity"
```

Expected: `1`

- [ ] **Step 5: Commit**

```bash
git add static/index.html
git commit -m "refactor: extract parseOffQuantity helper, use in onBarcodeScanned"
```

---

## Task 5: Add unit dimension helper

**Files:**
- Modify: `static/index.html`

For the quantity increment on known items, we need to know if two units are the same dimension (mass/volume/count) to decide whether conversion is possible. This is a small frontend lookup table.

- [ ] **Step 1: Add unitDimension helper immediately after parseOffQuantity**

```javascript
        unitDimension(unit) {
          const dims = {
            g: 'mass', kg: 'mass', oz: 'mass', lb: 'mass',
            ml: 'volume', L: 'volume', cup: 'volume', tbsp: 'volume', tsp: 'volume',
            piece: 'count', clove: 'count', can: 'count', jar: 'count', bunch: 'count',
          };
          return dims[unit] || null;
        },

```

- [ ] **Step 2: Add convertUnits helper immediately after unitDimension**

This converts a quantity from one unit to another using the same conversion factors as the Go backend.

```javascript
        convertUnits(qty, fromUnit, toUnit) {
          if (fromUnit === toUnit) return qty;
          const toBaseG = { g: 1, kg: 1000, oz: 28.3495, lb: 453.592 };
          const toBaseML = { ml: 1, L: 1000, cup: 236.588, tbsp: 14.787, tsp: 4.929 };
          const dimFrom = this.unitDimension(fromUnit);
          const dimTo = this.unitDimension(toUnit);
          if (!dimFrom || dimFrom !== dimTo || dimFrom === 'count') return null;
          if (dimFrom === 'mass') return qty * toBaseG[fromUnit] / toBaseG[toUnit];
          if (dimFrom === 'volume') return qty * toBaseML[fromUnit] / toBaseML[toUnit];
          return null;
        },

```

- [ ] **Step 3: Verify page loads**

```bash
curl -s http://localhost:8080/ | grep -c "unitDimension"
```

Expected: `1`

- [ ] **Step 4: Commit**

```bash
git add static/index.html
git commit -m "feat: add unitDimension and convertUnits helpers for grocery scanner"
```

---

## Task 6: Add grocery scanner methods

**Files:**
- Modify: `static/index.html`

Add `openGroceryScanner`, `closeGroceryScanner`, `onGroceryBarcodeScanned`, `resumeGroceryScanner`, `confirmGroceryNewItem`, `skipGroceryNewItem`.

- [ ] **Step 1: Add all grocery scanner methods after convertUnits**

```javascript
        async openGroceryScanner() {
          if (this._scannerActive) this.closeScanner();
          this.groceryScanOpen = true;
          this.groceryScanCount = 0;
          this.groceryScanPaused = false;
          this.groceryNewItem = null;
          await this.$nextTick();
          await this._startGroceryCamera();
        },

        async _startGroceryCamera() {
          if (!this.groceryScanOpen) return;
          const video = document.getElementById('grocery-scanner-video');
          try {
            const stream = await navigator.mediaDevices.getUserMedia({
              video: { facingMode: { ideal: 'environment' }, width: { ideal: 1920 }, height: { ideal: 1080 } }
            });
            if (!this.groceryScanOpen) { stream.getTracks().forEach(t => t.stop()); return; }
            this._stream = stream;
            video.srcObject = stream;
            await new Promise(resolve => { video.onloadedmetadata = resolve; });
            video.play();
          } catch {
            this.showToast('Camera access denied');
            return;
          }

          this._scannerActive = true;

          if (typeof BarcodeDetector !== 'undefined') {
            const detector = new BarcodeDetector({ formats: ['ean_13','ean_8','upc_a','upc_e','code_128','code_39','qr_code','itf','codabar'] });
            const scan = async () => {
              if (!this._scannerActive || this.groceryScanPaused) {
                if (this._scannerActive) this._scanRaf = requestAnimationFrame(scan);
                return;
              }
              try {
                const codes = await detector.detect(video);
                if (codes.length > 0) { this.onGroceryBarcodeScanned(codes[0].rawValue); return; }
              } catch { /* ignore */ }
              this._scanRaf = requestAnimationFrame(scan);
            };
            this._scanRaf = requestAnimationFrame(scan);
          } else {
            this._codeReader = new ZXing.BrowserMultiFormatReader();
            this._codeReader.decodeFromVideoElementContinuously(video, (result, err) => {
              if (result && !this.groceryScanPaused) this.onGroceryBarcodeScanned(result.getText());
            });
          }
        },

        closeGroceryScanner() {
          this.groceryScanOpen = false;
          this.groceryScanPaused = false;
          this.groceryNewItem = null;
          this._scannerActive = false;
          if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
          if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
          if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
        },

        async onGroceryBarcodeScanned(code) {
          if (!this.groceryScanOpen || this.groceryScanPaused) return;

          // Check inventory first
          try {
            const res = await fetch('/api/inventory/barcode/' + encodeURIComponent(code));
            if (res.ok) {
              const item = await res.json();
              // Determine increment amount from OFf
              let addQty = 1;
              let addUnit = item.unit;
              try {
                const off = await fetch('https://world.openfoodfacts.org/api/v2/product/' + encodeURIComponent(code) + '.json');
                if (off.ok) {
                  const data = await off.json();
                  if (data.status === 1 && data.product) {
                    const parsed = this.parseOffQuantity(data.product);
                    if (parsed) {
                      const targetUnit = item.preferred_unit || item.unit;
                      const converted = this.convertUnits(parsed.qty, parsed.unit, targetUnit);
                      if (converted !== null) {
                        addQty = converted;
                        addUnit = targetUnit;
                      } else {
                        // dimension mismatch — fall back to +1 in item's unit
                        addQty = 1;
                        addUnit = item.unit;
                      }
                    }
                  }
                }
              } catch { /* silent — use +1 default */ }

              const newQty = parseFloat((item.quantity + addQty).toFixed(4));
              try {
                const patch = await fetch(`/api/inventory/${item.id}`, {
                  method: 'PATCH',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({ quantity: newQty }),
                });
                if (patch.ok) {
                  this.groceryScanCount++;
                  this.showToast(`${item.name} +${addQty} ${addUnit}`);
                } else {
                  this.showToast(`Failed to update ${item.name}`);
                }
              } catch {
                this.showToast(`Failed to update ${item.name}`);
              }
              return;
            }
          } catch { /* fall through to new item flow */ }

          // Not in inventory — look up OFf and pause for confirmation
          this.groceryScanPaused = true;
          let prefill = { barcode: code, quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1 };

          try {
            const off = await fetch('https://world.openfoodfacts.org/api/v2/product/' + encodeURIComponent(code) + '.json');
            if (off.ok) {
              const data = await off.json();
              if (data.status === 1 && data.product) {
                const p = data.product;
                if (p.product_name) prefill.name = p.product_name;
                const parsed = this.parseOffQuantity(p);
                if (parsed) {
                  prefill.quantity = parsed.qty;
                  prefill.unit = parsed.unit;
                  prefill.preferred_unit = parsed.unit;
                }
              }
            }
          } catch { /* silent */ }

          // Try to pre-fill location from existing inventory item with same name
          if (prefill.name) {
            try {
              const inv = await fetch('/api/inventory/?name=' + encodeURIComponent(prefill.name));
              if (inv.ok) {
                const items = await inv.json();
                if (items.length > 0 && items[0].location) prefill.location = items[0].location;
              }
            } catch { /* silent */ }
          }

          this.groceryNewItem = prefill;
          this.groceryItemForm = { name: prefill.name || '', quantity: prefill.quantity, unit: prefill.unit, preferred_unit: prefill.preferred_unit, location: prefill.location, expiration_date: '', low_threshold: 1, barcode: code };
        },

        async confirmGroceryNewItem() {
          try {
            const res = await fetch('/api/inventory/', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(this.groceryItemForm),
            });
            if (res.ok) {
              this.groceryScanCount++;
              this.showToast(`Added ${this.groceryItemForm.name || 'item'} to inventory`);
            } else {
              this.showToast('Failed to add item');
            }
          } catch {
            this.showToast('Failed to add item');
          }
          this.skipGroceryNewItem();
        },

        skipGroceryNewItem() {
          this.groceryNewItem = null;
          this.groceryScanPaused = false;
          this.groceryItemForm = { name: '', quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1, barcode: '' };
        },

```

- [ ] **Step 2: Verify page loads**

```bash
curl -s http://localhost:8080/ | grep -c "openGroceryScanner"
```

Expected: `1`

- [ ] **Step 3: Commit**

```bash
git add static/index.html
git commit -m "feat: grocery scanner methods — increment known items, form for unknown"
```

---

## Task 7: Smoke test and verify

**Files:** none (verification only)

- [ ] **Step 1: Verify page loads cleanly in browser**

Start the server:
```bash
go run . &
sleep 1
```

Check the Shopping tab button is present:
```bash
curl -s http://localhost:8080/ | grep -c "Scan Groceries"
```
Expected: `1`

- [ ] **Step 2: Verify no JavaScript syntax errors**

```bash
curl -s http://localhost:8080/ | node --input-type=module -e "
const fs = require('fs');
process.stdin.resume();
let d = '';
process.stdin.on('data', c => d += c);
process.stdin.on('end', () => {
  // Just check it's non-empty HTML
  console.log(d.includes('groceryScanOpen') ? 'OK' : 'MISSING_STATE');
});" 2>/dev/null || curl -s http://localhost:8080/ | grep -c "groceryScanOpen"
```
Expected: `1` (state found in page)

- [ ] **Step 3: Verify the grocery scanner modal is in the DOM**

```bash
curl -s http://localhost:8080/ | grep -c "Grocery Scanner Modal"
```
Expected: `1`

- [ ] **Step 4: Kill test server**

```bash
pkill -f "go run" 2>/dev/null || true
```

- [ ] **Step 5: No commit needed** — verification only.
