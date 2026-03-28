# Barcode Scanner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add camera-based barcode scanning to the inventory system — scan to find/edit an existing item, or scan to pre-fill the barcode field when adding a new item, with automatic product lookup via Open Food Facts for unknown barcodes.

**Architecture:** A new `GET /api/inventory/barcode/{code}` backend endpoint does an exact match on the `barcode` column. The Alpine.js frontend integrates ZXing-js (CDN) for live camera decoding. Two entry points: a "Scan" button on the inventory tab header (find mode) and a scan icon next to the barcode field in the Add/Edit modal (fill mode). Unknown barcodes are looked up client-side against the Open Food Facts public API before opening the Add Item form.

**Tech Stack:** Go 1.22+ stdlib `net/http`, `modernc.org/sqlite`, ZXing-js `@zxing/library@0.19.1` (CDN UMD), Open Food Facts API (client-side, no key), Alpine.js (CDN, existing)

---

## File Map

| Action | File | Change |
|--------|------|--------|
| Modify | `handlers/inventory.go` | Add `GET /api/inventory/barcode/{code}` handler |
| Modify | `handlers/inventory_test.go` | Add `TestGetInventoryItemByBarcode` |
| Modify | `static/index.html` | Add ZXing CDN script, scanner state + methods, scanner modal, Scan button, barcode field in item modal |

---

## Task 1: Backend — `GET /api/inventory/barcode/{code}`

**Files:**
- Modify: `handlers/inventory.go`
- Modify: `handlers/inventory_test.go`

- [ ] **Step 1: Write the failing test**

Append to `handlers/inventory_test.go`:

```go
func TestGetInventoryItemByBarcode(t *testing.T) {
	mux, db := newMux(t)

	// Insert item with a known barcode
	_, err := db.Exec(`INSERT INTO inventory (name,quantity,unit,preferred_unit,location,low_threshold,expiration_date,barcode) VALUES ('Olive Oil',1,'L','','Pantry',1,'','5000157024671')`)
	if err != nil {
		t.Fatal(err)
	}

	// Found: exact barcode match → 200 + correct item
	req := httptest.NewRequest(http.MethodGet, "/api/inventory/barcode/5000157024671", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if item["name"] != "Olive Oil" {
		t.Errorf("expected name Olive Oil, got %v", item["name"])
	}
	if item["barcode"] != "5000157024671" {
		t.Errorf("expected barcode 5000157024671, got %v", item["barcode"])
	}

	// Not found: unknown barcode → 404
	req2 := httptest.NewRequest(http.MethodGet, "/api/inventory/barcode/9999999999999", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w2.Code, w2.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/josh/code_projects/kitchen_manager && go test ./handlers/... -run TestGetInventoryItemByBarcode -v
```

Expected: FAIL — 404 on the first request (route not registered yet).

- [ ] **Step 3: Add the handler to `handlers/inventory.go`**

In `RegisterInventory`, add this handler **after** `GET /api/inventory/suggestions` and **before** `GET /api/inventory/{id}` (literal paths must come before wildcard paths). The insertion point is between line 148 and line 150 in the current file:

```go
	mux.HandleFunc("GET /api/inventory/barcode/{code}", func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		if code == "" {
			WriteError(w, http.StatusBadRequest, "missing barcode")
			return
		}
		row := db.QueryRow(`SELECT id,name,quantity,unit,location,expiration_date,low_threshold,barcode,preferred_unit FROM inventory WHERE barcode=?`, code)
		item, err := scanInventoryRow(row)
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, item)
	})
```

Note: `r.PathValue("code")` is Go 1.22+ stdlib — no helper needed (unlike `pathIDFromPattern` which parses an int; this route uses a string).

- [ ] **Step 4: Run test to verify it passes**

```bash
cd /home/josh/code_projects/kitchen_manager && go test ./handlers/... -run TestGetInventoryItemByBarcode -v
```

Expected: PASS.

- [ ] **Step 5: Run full test suite**

```bash
cd /home/josh/code_projects/kitchen_manager && go test ./... -v 2>&1 | grep -E "^--- |^ok|^FAIL"
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/josh/code_projects/kitchen_manager && git add handlers/inventory.go handlers/inventory_test.go && git commit -m "feat: GET /api/inventory/barcode/{code} endpoint"
```

---

## Task 2: Frontend — Scanner state, modal, and ZXing integration

**Files:**
- Modify: `static/index.html`

This task adds the ZXing CDN script, all Alpine.js scanner state and methods, the scanner modal HTML, the "Scan" button on the inventory tab header, and the barcode field (with scan icon) in the Add/Edit Item modal.

### Key facts about the current file

- CDN scripts are in `<head>` (lines 7–8): Tailwind, then Alpine.js deferred
- Alpine.js `app()` data object is at line 401
- `itemForm` is at line 425: `{ name: '', quantity: 0, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1 }` — **`barcode` is missing and must be added**
- `openAddItem()` resets `itemForm` at line 509 — must include `barcode: ''`
- `editItem(item)` copies item into `itemForm` at line 519 — `barcode` will flow through the spread `{ ...item, ... }` automatically once it's in the DB response (it already is)
- `showToast(msg)` is at line 488 — add scanner methods before it
- Inventory tab "Add Item" button is in the inventory tab header — find it by searching for `openAddItem`
- The Add/Edit modal Cancel/Save buttons are at lines 265–267 — add barcode field just before them (after the low_threshold row)
- Other modals start at line 271 — the scanner modal goes after the item modal closing `</div>` at line 269

- [ ] **Step 1: Add ZXing CDN script to `<head>`**

Find:
```html
  <script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>
```

Replace with:
```html
  <script src="https://unpkg.com/@zxing/library@0.19.1/umd/index.min.js"></script>
  <script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>
```

ZXing must load **before** Alpine (and without `defer`) so it is available when Alpine initialises the component.

- [ ] **Step 2: Add scanner state to the Alpine.js data object**

Find:
```js
        _suggestionTimer: null,
        itemForm: { name: '', quantity: 0, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1 },
```

Replace with:
```js
        _suggestionTimer: null,
        scannerOpen: false,
        scannerMode: '',
        _codeReader: null,
        itemForm: { name: '', quantity: 0, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1, barcode: '' },
```

- [ ] **Step 3: Update `openAddItem()` to reset barcode**

Find:
```js
          this.itemForm = { name: '', quantity: 0, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1 };
```

Replace with:
```js
          this.itemForm = { name: '', quantity: 0, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 1, barcode: '' };
```

- [ ] **Step 4: Add `openScanner`, `closeScanner`, and `onBarcodeScanned` methods**

Find:
```js
        showToast(msg) {
```

Add these three methods immediately before it:

```js
        openScanner(mode) {
          this.scannerMode = mode;
          this.scannerOpen = true;
          this.$nextTick(() => {
            const hints = new Map();
            this._codeReader = new ZXing.BrowserMultiFormatReader(hints);
            this._codeReader.decodeFromConstraints(
              { video: { facingMode: 'environment' } },
              'scanner-video',
              (result, err) => {
                if (result) {
                  this.onBarcodeScanned(result.getText());
                }
              }
            ).catch((err) => {
              this.closeScanner();
              if (err && (err.name === 'NotAllowedError' || err.name === 'PermissionDeniedError')) {
                this.showToast('Camera access denied — enter barcode manually');
              }
            });
          });
        },

        closeScanner() {
          if (this._codeReader) {
            this._codeReader.reset();
            this._codeReader = null;
          }
          this.scannerOpen = false;
        },

        async onBarcodeScanned(code) {
          this.closeScanner();
          if (this.scannerMode === 'fill') {
            this.itemForm.barcode = code;
            return;
          }
          // scannerMode === 'find': look up in our inventory first
          try {
            const res = await fetch('/api/inventory/barcode/' + encodeURIComponent(code));
            if (res.ok) {
              const item = await res.json();
              this.editItem(item);
              return;
            }
          } catch {
            // fall through to Open Food Facts
          }
          // Not in inventory — try Open Food Facts
          let prefill = { barcode: code };
          try {
            const off = await fetch('https://world.openfoodfacts.org/api/v2/product/' + encodeURIComponent(code) + '.json');
            if (off.ok) {
              const data = await off.json();
              if (data.status === 1 && data.product) {
                const p = data.product;
                if (p.product_name) prefill.name = p.product_name;
              }
            }
          } catch {
            // silent fallback — open form with just barcode
          }
          this.openAddItem();
          Object.assign(this.itemForm, prefill);
        },

```

- [ ] **Step 5: Add the barcode field to the Add/Edit Item modal**

Find:
```html
      <div class="flex gap-2 pt-2">
        <button @click="modal = null" class="flex-1 border rounded-lg py-2 text-sm">Cancel</button>
        <button @click="saveItem()" class="flex-1 bg-green-600 text-white rounded-lg py-2 text-sm font-medium">Save</button>
      </div>
```

Replace with:
```html
      <div class="flex gap-2 items-center">
        <input x-model="itemForm.barcode"
               placeholder="Barcode"
               class="flex-1 border rounded-lg px-3 py-2 text-sm"
               autocomplete="off" />
        <button @click="openScanner('fill')"
                type="button"
                class="border rounded-lg px-3 py-2 text-sm text-gray-600 hover:bg-gray-50"
                title="Scan barcode">
          📷
        </button>
      </div>
      <div class="flex gap-2 pt-2">
        <button @click="modal = null" class="flex-1 border rounded-lg py-2 text-sm">Cancel</button>
        <button @click="saveItem()" class="flex-1 bg-green-600 text-white rounded-lg py-2 text-sm font-medium">Save</button>
      </div>
```

- [ ] **Step 6: Add the "Scan" button to the inventory tab header**

Find the inventory tab "Add Item" button. Search for `openAddItem` in the inventory tab section — it will look like:

```html
        <button @click="openAddItem()"
```

The inventory tab header has a title + Add Item button. Add a Scan button alongside it. Find:

```html
        <button @click="openAddItem()" class="bg-green-600 text-white px-3 py-1.5 rounded-lg text-sm font-medium">+ Add Item</button>
```

Replace with:
```html
        <button @click="openScanner('find')" class="border border-gray-300 text-gray-600 px-3 py-1.5 rounded-lg text-sm font-medium">📷 Scan</button>
        <button @click="openAddItem()" class="bg-green-600 text-white px-3 py-1.5 rounded-lg text-sm font-medium">+ Add Item</button>
```

- [ ] **Step 7: Add the scanner modal HTML**

Find the comment that marks the start of the shopping modal:
```html
  <!-- Add Shopping Item Modal -->
```

Insert the scanner modal immediately before it:

```html
  <!-- Scanner Modal -->
  <div x-show="scannerOpen" class="fixed inset-0 bg-black z-50 flex flex-col">
    <div class="flex items-center justify-between p-4">
      <span class="text-white text-sm font-medium">
        <span x-text="scannerMode === 'find' ? 'Scan to find item' : 'Scan barcode'"></span>
      </span>
      <button @click="closeScanner()" class="text-white text-sm border border-white/40 rounded-lg px-3 py-1.5">Cancel</button>
    </div>
    <div class="flex-1 relative overflow-hidden">
      <video id="scanner-video" class="absolute inset-0 w-full h-full object-cover" playsinline></video>
      <div class="absolute inset-0 flex items-center justify-center pointer-events-none">
        <div class="w-64 h-40 border-2 border-white/70 rounded-lg"></div>
      </div>
    </div>
    <div class="p-4 text-center">
      <p class="text-white/70 text-sm">Point camera at a barcode</p>
    </div>
  </div>

```

Note: `playsinline` is required on iOS Safari to prevent the video from going fullscreen.

- [ ] **Step 8: Verify build compiles**

```bash
cd /home/josh/code_projects/kitchen_manager && go build ./... && echo "Build OK"
```

Expected: `Build OK`

- [ ] **Step 9: Run full test suite**

```bash
cd /home/josh/code_projects/kitchen_manager && go test ./... -v 2>&1 | grep -E "^--- |^ok|^FAIL"
```

Expected: all PASS.

- [ ] **Step 10: Commit**

```bash
cd /home/josh/code_projects/kitchen_manager && git add static/index.html && git commit -m "feat: barcode scanner with ZXing-js; Open Food Facts lookup; barcode field in item modal"
```

---

## Manual Testing Checklist (on a mobile device)

These are not automated — test them by opening the app in a mobile browser (or desktop with camera):

- [ ] Open inventory tab → "📷 Scan" button visible next to "+ Add Item"
- [ ] Tap "📷 Scan" → browser requests camera permission → grant → scanner modal opens with live viewfinder
- [ ] Point at a barcode of an item already in inventory → scanner closes, Edit Item modal opens pre-filled
- [ ] Point at an unknown barcode of a grocery product → scanner closes, Add Item modal opens with name + barcode pre-filled (from Open Food Facts)
- [ ] Point at a completely unknown barcode with no Open Food Facts entry → Add Item modal opens with only barcode pre-filled
- [ ] Tap Cancel during scan → scanner closes, nothing else happens
- [ ] Open Add Item modal → barcode field visible → tap 📷 icon → scanner opens → scan → barcode field filled, scanner closes, modal stays open
- [ ] Deny camera permission → scanner modal closes, toast "Camera access denied — enter barcode manually" shown
