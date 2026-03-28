# Inventory Autocomplete Design

**Date:** 2026-03-27
**Status:** Approved

## Overview

Add autocomplete to the inventory add/edit modal. As the user types a name, a dropdown shows matching items from existing inventory. Selecting a suggestion pre-fills name, unit, preferred_unit, location, and low_threshold — leaving quantity blank.

Location field is also upgraded from a hardcoded list to a dynamic list derived from actual inventory values.

## Backend — `GET /api/inventory/suggestions`

New endpoint registered in `handlers/inventory.go`:

```
GET /api/inventory/suggestions?q=past
```

- Case-insensitive prefix match on `name` using SQLite `LIKE ?` with value `q%`
- Returns up to 10 results
- Empty or absent `q` returns all distinct items (used to seed the locations list on app init)
- Response shape:

```json
[
  {"name":"Pasta","unit":"g","preferred_unit":"kg","location":"Pantry","low_threshold":1},
  {"name":"Pasta Sauce","unit":"jar","preferred_unit":"","location":"Fridge","low_threshold":2}
]
```

Query:
```sql
SELECT DISTINCT name, unit, preferred_unit, location, low_threshold
FROM inventory
WHERE name LIKE ?
ORDER BY name
LIMIT 10
```

## Frontend — Alpine.js Changes

### New data properties

```js
suggestions: [],
showSuggestions: false,
```

### New methods

```js
async fetchSuggestions() {
  const q = this.itemForm.name;
  if (!q) { this.suggestions = []; this.showSuggestions = false; return; }
  const res = await fetch('/api/inventory/suggestions?q=' + encodeURIComponent(q));
  this.suggestions = await res.json();
  this.showSuggestions = this.suggestions.length > 0;
},

selectSuggestion(s) {
  this.itemForm.name = s.name;
  this.itemForm.unit = s.unit;
  this.itemForm.preferred_unit = s.preferred_unit;
  this.itemForm.location = s.location;
  this.itemForm.low_threshold = s.low_threshold;
  this.showSuggestions = false;
},
```

### Init change

On `init()`, fetch all suggestions (no `q`) to seed the dynamic locations list:

```js
const sRes = await fetch('/api/inventory/suggestions');
const allItems = await sRes.json();
this.locations = [...new Set(allItems.map(i => i.location).filter(Boolean))];
```

Remove the hardcoded `locations` array.

### Name input changes

```html
<input
  x-model="itemForm.name"
  @input="fetchSuggestions()"
  @keydown.escape="showSuggestions = false"
  @blur="setTimeout(() => showSuggestions = false, 150)"
  placeholder="Name *"
  class="w-full border rounded-lg px-3 py-2 text-sm"
  autocomplete="off"
/>
<div x-show="showSuggestions && suggestions.length" class="...dropdown styles...">
  <template x-for="s in suggestions" :key="s.name">
    <div @click="selectSuggestion(s)" class="...row styles...">
      <span x-text="s.name"></span>
      <span x-text="s.location + ' · ' + s.unit" class="hint"></span>
    </div>
  </template>
</div>
```

The blur uses a 150ms delay so a click on a suggestion row fires before the input loses focus.

### Location field

`locations` array changes from hardcoded `['fridge', 'freezer', 'pantry', 'cabinet', 'other']` to dynamically populated from the suggestions endpoint on init. The `<select>` markup stays the same.

## Testing

### New: `TestInventorySuggestions` in `handlers/inventory_test.go`

- Insert "Pasta" (unit=g, location=Pantry) and "Pasta Sauce" (unit=jar, location=Fridge)
- `GET /api/inventory/suggestions?q=past` → 2 results, both names present, correct unit and location
- `GET /api/inventory/suggestions?q=pasta+s` → 1 result ("Pasta Sauce")
- `GET /api/inventory/suggestions` (no q) → 2 results (all items)
