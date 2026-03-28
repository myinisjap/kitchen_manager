# Scan Feedback Enhanced Pill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the plain gray toast used for grocery scan results with a styled green/red pill showing item name, delta, and new total (success) or an error message (error), while leaving all other app toasts unchanged.

**Architecture:** Extend the existing `showToast(msg)` method with two optional parameters (`style`, `badge`). Add `toastStyle` and `toastBadge` to Alpine state. Replace the static toast element HTML with a dynamic class binding. Update grocery scanner call-sites to pass the new arguments. All existing callers outside the grocery scanner remain untouched.

**Tech Stack:** Alpine.js (x-bind :class, x-show, x-text), Tailwind CSS utility classes, single HTML file (`static/index.html`).

---

### Task 1: Extend toast state, method, and element

**Files:**
- Modify: `static/index.html:510-512` (toast element HTML)
- Modify: `static/index.html:526` (state declaration)
- Modify: `static/index.html:974-977` (showToast method)

This task has no backend — no Go tests apply. Verify visually by loading the app and triggering any existing toast (e.g. add an item to the shopping list) to confirm the gray style still works.

- [ ] **Step 1: Add `toastStyle` and `toastBadge` to Alpine state**

Find line 526 in `static/index.html`:
```js
        toast: '',
```
Replace with:
```js
        toast: '',
        toastStyle: '',
        toastBadge: '',
```

- [ ] **Step 2: Replace the toast element with the dynamic version**

Find lines 510-512:
```html
  <!-- Toast notification -->
  <div x-show="toast" x-transition
       class="fixed top-16 left-1/2 -translate-x-1/2 bg-gray-900 text-white text-sm px-4 py-2 rounded-full z-30 shadow-lg"
       x-text="toast"></div>
```
Replace with:
```html
  <!-- Toast notification -->
  <div x-show="toast" x-transition
       :class="{
         'bg-green-600': toastStyle === 'success',
         'bg-red-600':   toastStyle === 'error',
         'bg-gray-900':  toastStyle === ''
       }"
       class="fixed top-16 left-1/2 -translate-x-1/2 text-white text-sm px-4 py-2 rounded-full z-30 shadow-lg flex items-center gap-2">
    <span x-show="toastStyle === 'success'">✓</span>
    <span x-show="toastStyle === 'error'">✕</span>
    <span x-text="toast"></span>
    <span x-show="toastStyle === 'success' && toastBadge"
          x-text="toastBadge"
          class="bg-white/20 rounded-full px-2 py-0.5 text-xs font-semibold ml-1"></span>
  </div>
```

- [ ] **Step 3: Update `showToast` to accept style and badge**

Find lines 974-977:
```js
        showToast(msg) {
          this.toast = msg;
          setTimeout(() => this.toast = '', 2500);
        },
```
Replace with:
```js
        showToast(msg, style = '', badge = '') {
          this.toast = msg;
          this.toastStyle = style;
          this.toastBadge = badge;
          setTimeout(() => { this.toast = ''; this.toastStyle = ''; this.toastBadge = ''; }, 2500);
        },
```

- [ ] **Step 4: Verify existing toasts still work**

Run: `go run . &` then open `http://localhost:8080` in a browser.
- Navigate to Shopping tab → click "+ Add" → add an item → click "Add to list"
- Expected: gray rounded pill appears at top center saying "Added to list"
- Navigate to Inventory tab → delete any item
- Expected: gray rounded pill says "Item deleted"

Kill the server (`kill %1` or Ctrl-C).

- [ ] **Step 5: Commit**

```bash
git add static/index.html
git commit -m "feat: extend showToast with style and badge params, dynamic toast element"
```

---

### Task 2: Update grocery scanner call-sites

**Files:**
- Modify: `static/index.html:820` (BarcodeDetector scan error toast)
- Modify: `static/index.html:834` (ZXing scan error toast)
- Modify: `static/index.html:897` (known item success toast)
- Modify: `static/index.html:900` (known item PATCH failure — non-ok response)
- Modify: `static/index.html:903` (known item PATCH failure — network catch)
- Modify: `static/index.html:960` (new item POST failure — non-ok response)
- Modify: `static/index.html:963` (new item POST failure — network catch)

Line numbers may have shifted by ±3 after Task 1 edits. Search for the exact strings below.

- [ ] **Step 1: Update the two scan-error toasts (BarcodeDetector and ZXing paths)**

Find (appears twice, once around line 820 and once around line 834):
```js
this.showToast("Couldn't read barcode");
```
Replace both occurrences with:
```js
this.showToast("Couldn't read barcode", 'error');
```

- [ ] **Step 2: Update the known-item success toast**

Find (around line 897):
```js
                  const displayQty = Number.isInteger(addQty) ? addQty : parseFloat(addQty.toFixed(2));
                  this.showToast(`${item.name} +${displayQty} ${addUnit}`);
```
Replace with:
```js
                  const displayQty = Number.isInteger(addQty) ? addQty : parseFloat(addQty.toFixed(2));
                  const displayNew = Number.isInteger(newQty) ? newQty : parseFloat(newQty.toFixed(2));
                  this.showToast(`${item.name} +${displayQty} ${addUnit}`, 'success', `${displayNew} ${item.unit}`);
```

- [ ] **Step 3: Update the two known-item PATCH failure toasts**

Find (around line 900 — inside `if (!patch.ok)`):
```js
                  this.showToast(`Failed to update ${item.name}`);
```
Replace with:
```js
                  this.showToast(`Failed to update ${item.name}`, 'error');
```

Find (around line 903 — inside outer `catch`):
```js
                this.showToast(`Failed to update ${item.name}`);
```
Replace with:
```js
                this.showToast(`Failed to update ${item.name}`, 'error');
```

- [ ] **Step 4: Update the two new-item POST failure toasts**

Find (around line 960 — inside `if (!res.ok)`):
```js
              this.showToast('Failed to add item');
```
Replace with:
```js
              this.showToast('Failed to add item', 'error');
```

Find (around line 963 — inside outer `catch`):
```js
            this.showToast('Failed to add item');
```
Replace with:
```js
            this.showToast('Failed to add item', 'error');
```

- [ ] **Step 5: Verify the success and error pills**

Run: `go run . &` then open `http://localhost:8080`.

**Test success pill:**
- Navigate to Shopping tab → click "Scan Groceries"
- Scan a barcode for a known inventory item
- Expected: green pill at top center with ✓, item name, `+{qty} {unit}`, and a faint badge showing the new total

**Test error pill:**
- With the grocery scanner still open, hold a blank surface to the camera for a few seconds
- Expected: red pill at top center with ✕ and "Couldn't read barcode"

**Test gray pill unchanged:**
- Close the scanner → navigate to Inventory tab → add or delete an item
- Expected: same gray pill as before, no icons, no badge

Kill the server.

- [ ] **Step 6: Run Go tests to confirm no backend regressions**

```bash
go test ./...
```
Expected: all packages pass, no failures.

- [ ] **Step 7: Commit**

```bash
git add static/index.html
git commit -m "feat: styled success/error pills for grocery scan feedback"
```
