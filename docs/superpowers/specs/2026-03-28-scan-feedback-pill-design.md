# Scan Feedback Enhanced Pill Design

**Date:** 2026-03-28

## Goal

Replace the plain gray toast used for grocery scan results with a richer, styled pill that shows item name, amount added, and new total on success — and a red pill with a clear message on error. All other toasts in the app (inventory edits, shopping list, recipes, calendar) remain unchanged.

---

## Scope

Only the grocery scanner modal's feedback is changing. The existing `showToast(msg)` method is extended to accept an optional `style` parameter. All existing callers continue to call `showToast(msg)` with no second argument and get the current gray pill. Only grocery scanner calls pass `'success'` or `'error'`.

---

## UI Design

### Success pill (known item incremented)

```
✓  Whole Milk   +2 L   [4.5 L]
```

- Green background (`bg-green-600`)
- Checkmark icon on the left
- Item name, bold
- `+{displayQty} {addUnit}` in slightly muted text
- New total in a white/transparent badge on the right: `{newQty} {item.unit}`
- Same position as current toast: `fixed top-16 left-1/2 -translate-x-1/2`, `z-30`
- Same 2.5s auto-dismiss via `showToast`

### Error pill

```
✕  Couldn't read barcode
✕  Failed to update Eggs
✕  Failed to add item
```

- Red background (`bg-red-600`)
- ✕ icon on the left
- Error message text
- No badge (no total to show)
- Same position and 2.5s dismiss

### Default pill (unchanged)

All non-grocery toasts continue to use the current gray (`bg-gray-900`) style.

---

## Implementation

### State additions

Add `toastStyle: ''` to Alpine state alongside `toast: ''`. Valid values: `''` (default gray), `'success'` (green), `'error'` (red).

### `showToast(msg, style = '')` signature change

```js
showToast(msg, style = '') {
  this.toast = msg;
  this.toastStyle = style;
  setTimeout(() => { this.toast = ''; this.toastStyle = ''; }, 2500);
}
```

### Toast element class binding

Replace the static `class="... bg-gray-900 ..."` with a dynamic binding:

```html
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

Add `toastBadge: ''` to state. `showToast` gains an optional third parameter `badge = ''`:

```js
showToast(msg, style = '', badge = '') {
  this.toast = msg;
  this.toastStyle = style;
  this.toastBadge = badge;
  setTimeout(() => { this.toast = ''; this.toastStyle = ''; this.toastBadge = ''; }, 2500);
}
```

### Grocery scanner call-sites

**Known item success** (currently line ~897):
```js
const displayQty = Number.isInteger(addQty) ? addQty : parseFloat(addQty.toFixed(2));
const displayNew = Number.isInteger(newQty) ? newQty : parseFloat(newQty.toFixed(2));
this.showToast(`${item.name}  +${displayQty} ${addUnit}`, 'success', `${displayNew} ${item.unit}`);
```

**Known item PATCH failure** (currently line ~900, ~903):
```js
this.showToast(`Failed to update ${item.name}`, 'error');
```

**Unreadable barcode** (currently lines ~820, ~834):
```js
this.showToast("Couldn't read barcode", 'error');
```

**New item add failure** (currently lines ~960, ~963):
```js
this.showToast('Failed to add item', 'error');
```

**New item add success** (currently line ~957) — keep gray (it's a confirmation, not a scan increment):
```js
this.showToast(`Added ${this.groceryItemForm.name || 'item'} to inventory`);
```

---

## What Does NOT Change

- All existing `showToast(msg)` calls outside the grocery scanner
- Toast position, timing, or animation
- Backend / Go code
- Any other modal or UI component

---

## Error Handling

No new error paths. This is purely a visual change to existing feedback.
