# Grocery Scanner Review List Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace instant-PATCH-on-scan with a pending list that the user reviews and commits after tapping "Done", and throttle the scan rate to 2.5s between scans.

**Architecture:** All changes are in `static/index.html` only. New Alpine state (`groceryPendingItems`, `groceryScanReview`, `_scanCooldown`) drives two modal views — scan view and review view. `onGroceryBarcodeScanned` accumulates into the pending list instead of PATCHing. Two new methods: `finishGroceryScan` (Done button) and `commitGroceryItems` (Update Inventory button).

**Tech Stack:** Alpine.js reactive state, Tailwind CSS, single HTML file. No backend changes.

---

### Task 1: Update Alpine state

**Files:**
- Modify: `static/index.html:562-568`

- [ ] **Step 1: Replace grocery scanner state block**

Find these exact lines (lines 562–568):
```js
        groceryScanOpen: false,
        groceryScanCount: 0,
        groceryScanPaused: false,
        _groceryProcessing: false,
        groceryNewItem: null, // prefill object when paused for unknown item
        groceryCameraError: '',
        groceryItemForm: { name: '', quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1, barcode: '' },
```
Replace with:
```js
        groceryScanOpen: false,
        groceryScanPaused: false,
        groceryScanReview: false,
        groceryPendingItems: [],
        _groceryProcessing: false,
        _scanCooldown: false,
        groceryNewItem: null, // prefill object when paused for unknown item
        groceryCameraError: '',
        groceryItemForm: { name: '', quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1, barcode: '' },
```

Note: `groceryScanCount` is removed. `groceryScanReview`, `groceryPendingItems`, and `_scanCooldown` are added.

- [ ] **Step 2: Verify the file loads without errors**

Run: `go run . &` — open `http://localhost:8080` in browser. Check browser console for Alpine errors.

Kill the server (`kill %1`).

- [ ] **Step 3: Commit**

```bash
git add static/index.html
git commit -m "feat: add groceryPendingItems, groceryScanReview, _scanCooldown state"
```

---

### Task 2: Update modal HTML

**Files:**
- Modify: `static/index.html:287-364`

This task updates the grocery scanner modal HTML. The modal currently has: header, camera section, new-item form. After this task it will have: header (updated badge + Done handler), camera section (wrapped in `x-show="!groceryScanReview"`), new-item form (unchanged), and a new review section.

- [ ] **Step 1: Update the header badge and Done button**

Find (lines 291–296):
```html
      <div class="flex items-center gap-3">
        <span class="text-white text-sm font-medium">Scan Groceries</span>
        <span x-show="groceryScanCount > 0"
              class="bg-white/20 text-white text-xs px-2 py-0.5 rounded-full"
              x-text="groceryScanCount + ' scanned'"></span>
      </div>
      <button @click="closeGroceryScanner()" class="text-white text-sm border border-white/40 rounded-lg px-3 py-1.5">Done</button>
```
Replace with:
```html
      <div class="flex items-center gap-3">
        <span class="text-white text-sm font-medium" x-text="groceryScanReview ? 'Review Scanned Items' : 'Scan Groceries'"></span>
        <span x-show="groceryPendingItems.length > 0 && !groceryScanReview"
              class="bg-white/20 text-white text-xs px-2 py-0.5 rounded-full"
              x-text="groceryPendingItems.length + ' pending'"></span>
      </div>
      <button @click="finishGroceryScan()" class="text-white text-sm border border-white/40 rounded-lg px-3 py-1.5">Done</button>
```

- [ ] **Step 2: Wrap the camera section in x-show**

Find (line 299):
```html
    <!-- Camera -->
    <div class="relative overflow-hidden" :class="groceryScanPaused ? 'h-48' : 'flex-1'">
```
Replace with:
```html
    <!-- Camera -->
    <div x-show="!groceryScanReview" class="relative overflow-hidden" :class="groceryScanPaused ? 'h-48' : 'flex-1'">
```

- [ ] **Step 3: Add review section before the closing `</div>` of the modal**

Find (lines 363–364):
```html
    </div>
  </div>

  <!-- Scanner Modal -->
```
Replace with:
```html
    </div>

    <!-- Review section -->
    <div x-show="groceryScanReview" class="flex-1 flex flex-col bg-white">
      <div class="flex-1 overflow-y-auto p-4">
        <div x-show="groceryPendingItems.length === 0" class="text-center text-gray-400 py-8 text-sm">No items to update</div>
        <template x-for="(pending, i) in groceryPendingItems" :key="pending.barcode">
          <div class="flex items-center justify-between py-3 border-b border-gray-100 last:border-0">
            <div>
              <span class="font-medium text-gray-800 text-sm" x-text="pending.name"></span>
              <span class="text-gray-500 text-sm ml-2" x-text="'+' + pending.qty + ' ' + pending.unit"></span>
            </div>
            <button @click="groceryPendingItems.splice(i, 1)" class="text-gray-400 hover:text-red-500 text-lg leading-none px-2">✕</button>
          </div>
        </template>
      </div>
      <div class="p-4 flex gap-2 border-t border-gray-200">
        <button @click="commitGroceryItems()"
                class="flex-1 bg-green-600 text-white rounded-lg py-2 text-sm font-medium">
          Update Inventory
        </button>
        <button @click="closeGroceryScanner()"
                class="flex-1 border border-gray-300 text-gray-600 rounded-lg py-2 text-sm font-medium">
          Cancel
        </button>
      </div>
    </div>
  </div>

  <!-- Scanner Modal -->
```

- [ ] **Step 4: Verify HTML renders**

Run: `go run . &` — open `http://localhost:8080`, go to Shopping tab, tap "Scan Groceries". Confirm modal opens, camera starts, header shows "Scan Groceries". Tap "Done" — modal should close (no pending items). Kill server.

- [ ] **Step 5: Commit**

```bash
git add static/index.html
git commit -m "feat: grocery scanner review view HTML — pending list and Update Inventory button"
```

---

### Task 3: Rewrite `onGroceryBarcodeScanned` and add `finishGroceryScan` + `commitGroceryItems`

**Files:**
- Modify: `static/index.html` — `openGroceryScanner`, `closeGroceryScanner`, `onGroceryBarcodeScanned`, add `finishGroceryScan`, add `commitGroceryItems`

This is the main logic task. Read the current methods carefully before editing.

- [ ] **Step 1: Update `openGroceryScanner` to reset new state**

Find inside `openGroceryScanner()` (around line 793–799):
```js
          this.groceryScanOpen = true;
          this.groceryScanCount = 0;
          this.groceryScanPaused = false;
          this._groceryProcessing = false;
          this.groceryNewItem = null;
          this.groceryCameraError = '';
```
Replace with:
```js
          this.groceryScanOpen = true;
          this.groceryScanPaused = false;
          this.groceryScanReview = false;
          this.groceryPendingItems = [];
          this._groceryProcessing = false;
          this._scanCooldown = false;
          this.groceryNewItem = null;
          this.groceryCameraError = '';
```

- [ ] **Step 2: Update `closeGroceryScanner` to reset new state**

Find inside `closeGroceryScanner()` (around lines 859–865):
```js
          this.groceryScanOpen = false;
          this.groceryScanPaused = false;
          this.groceryNewItem = null;
```
Replace with:
```js
          this.groceryScanOpen = false;
          this.groceryScanPaused = false;
          this.groceryScanReview = false;
          this.groceryPendingItems = [];
          this._scanCooldown = false;
          this.groceryNewItem = null;
```

- [ ] **Step 3: Replace `onGroceryBarcodeScanned`**

Find the entire `onGroceryBarcodeScanned` method. It starts with:
```js
        async onGroceryBarcodeScanned(code) {
          if (!this.groceryScanOpen || this.groceryScanPaused || this._groceryProcessing) return;
```
And ends just before the `confirmGroceryNewItem` method. Replace the entire method with:

```js
        async onGroceryBarcodeScanned(code) {
          if (!this.groceryScanOpen || this.groceryScanPaused || this._groceryProcessing || this._scanCooldown) return;
          this._groceryProcessing = true;
          this._scanCooldown = true;
          setTimeout(() => { this._scanCooldown = false; }, 2500);

          try {
            const res = await fetch('/api/inventory/barcode/' + encodeURIComponent(code));
            if (res.ok) {
              const item = await res.json();
              // Add to or update pending list
              const existing = this.groceryPendingItems.find(p => p.barcode === code);
              if (existing) {
                existing.qty = parseFloat((existing.qty + 1).toFixed(4));
              } else {
                this.groceryPendingItems.push({ id: item.id, name: item.name, qty: 1, unit: item.unit, barcode: code });
              }
              const entry = this.groceryPendingItems.find(p => p.barcode === code);
              this.showToast(`${item.name} +1 ${item.unit}`, 'success', `${entry.qty} ${item.unit}`);
              this._groceryProcessing = false;

              // OFf refinement in background (non-blocking)
              (async () => {
                try {
                  const offPromise = fetch('https://world.openfoodfacts.org/api/v2/product/' + encodeURIComponent(code) + '.json').then(r => r.json());
                  const offData = await Promise.race([offPromise, new Promise((_, r) => setTimeout(() => r(new Error('timeout')), 3000))]);
                  if (offData && offData.status === 1 && offData.product) {
                    const parsed = this.parseOffQuantity(offData.product);
                    if (parsed) {
                      const targetUnit = item.preferred_unit || item.unit;
                      const converted = this.convertUnits(parsed.qty, parsed.unit, targetUnit);
                      if (converted !== null && converted !== 1) {
                        const pendingEntry = this.groceryPendingItems.find(p => p.barcode === code);
                        if (pendingEntry) {
                          // Replace initial 1 with OFf quantity (first scan only — if qty > 1 it was scanned multiple times)
                          if (pendingEntry.qty === 1) {
                            pendingEntry.qty = parseFloat(converted.toFixed(4));
                            pendingEntry.unit = targetUnit;
                          }
                        }
                      }
                    }
                  }
                } catch { /* silent */ }
              })();
              return;
            }
          } catch { /* fall through to new item flow */ }

          // Not in inventory — pause for unknown item form
          this._groceryProcessing = false;
          this.groceryScanPaused = true;
          let prefill = { barcode: code, quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1 };

          try {
            const offPromise = fetch(`https://world.openfoodfacts.org/api/v2/product/${code}.json`).then(r => r.json());
            const offTimeout = new Promise((_, reject) => setTimeout(() => reject(new Error('timeout')), 3000));
            let offData = null;
            try { offData = await Promise.race([offPromise, offTimeout]); } catch { /* silent fallback */ }
            if (offData && offData.status === 1 && offData.product) {
              const p = offData.product;
              if (p.product_name) prefill.name = p.product_name;
              const parsed = this.parseOffQuantity(p);
              if (parsed) {
                prefill.quantity = parsed.qty;
                prefill.unit = parsed.unit;
                prefill.preferred_unit = parsed.unit;
              }
            }
          } catch { /* silent */ }

          // Location pre-fill from existing inventory
          if (prefill.name) {
            try {
              const invRes = await fetch('/api/inventory/?name=' + encodeURIComponent(prefill.name));
              if (invRes.ok) {
                const invData = await invRes.json();
                if (invData && invData.length > 0 && invData[0].location) prefill.location = invData[0].location;
              }
            } catch { /* silent */ }
          }

          this.groceryNewItem = prefill;
          this.groceryItemForm = { name: prefill.name || '', quantity: prefill.quantity, unit: prefill.unit, preferred_unit: prefill.preferred_unit, location: prefill.location, expiration_date: '', low_threshold: 1, barcode: code };
        },
```

- [ ] **Step 4: Add `finishGroceryScan` after `closeGroceryScanner`**

Find the line that starts `closeGroceryScanner()` and ends its method. After the closing `},` of `closeGroceryScanner`, add:

```js
        finishGroceryScan() {
          if (this.groceryPendingItems.length === 0) {
            this.closeGroceryScanner();
            return;
          }
          this._scannerActive = false;
          if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
          if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
          if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
          this.groceryScanReview = true;
        },
```

- [ ] **Step 5: Add `commitGroceryItems` after `finishGroceryScan`**

```js
        async commitGroceryItems() {
          const count = this.groceryPendingItems.length;
          for (const pending of this.groceryPendingItems) {
            try {
              const cur = await fetch('/api/inventory/barcode/' + encodeURIComponent(pending.barcode));
              if (!cur.ok) continue;
              const item = await cur.json();
              const newQty = parseFloat((item.quantity + pending.qty).toFixed(4));
              await fetch(`/api/inventory/${pending.id}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ quantity: newQty }),
              });
            } catch { /* skip failed items silently */ }
          }
          this.fetchInventory();
          this.closeGroceryScanner();
          this.showToast(`Updated ${count} item(s)`);
        },
```

- [ ] **Step 6: Update `confirmGroceryNewItem` to remove `groceryScanCount` reference**

Find inside `confirmGroceryNewItem`:
```js
              this.groceryScanCount++;
              this.showToast(`Added ${this.groceryItemForm.name || 'item'} to inventory`);
```
Replace with:
```js
              this.showToast(`Added ${this.groceryItemForm.name || 'item'} to inventory`);
```

- [ ] **Step 7: Verify no remaining `groceryScanCount` references**

Run:
```bash
grep -n "groceryScanCount" static/index.html
```
Expected: no output (zero matches).

- [ ] **Step 8: Run Go tests**

```bash
go test ./...
```
Expected: all pass.

- [ ] **Step 9: Smoke test**

Run: `go run . &` — open `http://localhost:8080`, Shopping tab → Scan Groceries.

- Scan a known item → green pill appears, badge shows "1 pending", scanner stays open
- Scan the same item again → badge shows "1 pending" (same entry, qty incremented), new pill
- Tap "Done" → review view appears with item list
- Tap "Update Inventory" → modal closes, toast "Updated 1 item(s)"

Kill server.

- [ ] **Step 10: Commit**

```bash
git add static/index.html
git commit -m "feat: grocery scanner review list — accumulate scans, commit on Update Inventory"
```
