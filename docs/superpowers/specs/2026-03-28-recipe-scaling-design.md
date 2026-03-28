# Recipe Scaling with Serving Count Config

**Date:** 2026-03-28

## Goal

Let users scale recipe ingredient quantities at the point of adding them to the shopping list. A global default serving count (stored in a new `settings` table) drives the initial scale value for every recipe card. Each recipe card gets a numeric stepper that overrides the default for that session. When "Add to shopping list" is clicked the backend multiplies all ingredient quantities by the ratio `selected_servings / recipe_servings` before computing the shopping list shortfall.

---

## What Does NOT Change

- The `recipes` table schema — `servings INTEGER NOT NULL DEFAULT 1` is already present.
- `GET /api/recipes/` and `GET /api/recipes/{id}` — response shapes are unchanged.
- `POST /api/recipes/` and `PATCH /api/recipes/{id}` — servings is already an accepted field.
- The shortfall calculation logic inside `POST /api/recipes/{id}/add-to-shopping-list` — it already applies a scale factor; we are only changing how that scale factor is communicated.
- The existing `servings` query parameter on `add-to-shopping-list` — it is replaced by a JSON body field (see below), but the internal `scale = requestedServings / recipeServings` math is unchanged.
- All other tabs (Inventory, Shopping, Calendar).
- The `settings` table is new — no existing table is altered.
- No existing Go handler files are renamed or restructured.

---

## Settings Table Schema

Add to `createSchema()` in `db.go`:

```sql
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
```

Seed the default row in the same startup block (using `INSERT OR IGNORE` so it is a no-op on subsequent starts):

```sql
INSERT OR IGNORE INTO settings (key, value) VALUES ('default_servings', '2');
```

Both statements run inside the existing `db.Exec(...)` call that creates the schema. No separate migration is needed because `CREATE TABLE IF NOT EXISTS` is idempotent.

---

## New Handler File: `handlers/settings.go`

A new file keeps settings logic isolated from recipes and avoids growing `recipes.go` further.

### `RegisterSettings(mux *http.ServeMux, db *sql.DB)`

Register two routes:

#### `GET /api/settings`

Returns all rows from `settings` as a flat JSON object:

```json
{ "default_servings": "2" }
```

Query: `SELECT key, value FROM settings`

Builds a `map[string]string`, serialises with `WriteJSON(w, http.StatusOK, m)`.

#### `PATCH /api/settings`

Accepts a JSON object of key/value pairs. For each pair, upserts:

```sql
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value
```

Responds with the full updated settings object (same shape as `GET /api/settings`).

Validation: values must be non-empty strings; unrecognised keys are accepted (forward-compatible). If `default_servings` is present its value must parse as a positive integer — otherwise respond `400 Bad Request` with `{"error": "default_servings must be a positive integer"}`.

---

## `main.go` Change

Add one line to wire the new handler:

```go
handlers.RegisterSettings(mux, db)
```

Place it alongside the existing `handlers.Register*` calls.

---

## `POST /api/recipes/{id}/add-to-shopping-list` Change

**Current behaviour:** reads `servings` from the URL query string (`r.URL.Query().Get("servings")`).

**New behaviour:** reads `servings` from a JSON request body. The query-string parameter is removed.

New body struct (decoded with `ReadJSON`):

```go
var body struct {
    Servings int `json:"servings"`
}
// ignore decode error — body is optional
_ = ReadJSON(r, &body)
requestedServings := body.Servings
if requestedServings <= 0 {
    requestedServings = 1
}
```

Everything else in the handler (recipe lookup, `scale` computation, ingredient loop, shortfall insertion) is unchanged.

**Why JSON body instead of query param?** Consistency with all other mutating endpoints in this codebase; no change to the scale math.

---

## Alpine.js State Additions

Add the following properties to the `app()` return object, alongside the existing recipe state block:

```js
// Settings
settings: {},           // populated from GET /api/settings on init

// Per-recipe session scaling
recipeServings: {},     // map of recipe.id → current stepper value (integer)
```

`recipeServings` is a plain object used as a map. It is never persisted. It resets when the page reloads.

`settings` is loaded once on `init()` and re-fetched whenever the settings form is saved.

---

## `init()` Change

Add a `fetchSettings()` call alongside the existing fetches:

```js
async init() {
  // ... existing calls ...
  await this.fetchSettings();
  // ... rest unchanged ...
},
```

---

## New Methods

### `fetchSettings()`

```js
async fetchSettings() {
  const res = await fetch('/api/settings');
  this.settings = await res.json();
},
```

### `saveSettings()`

Called by the settings form save button:

```js
async saveSettings() {
  const res = await fetch('/api/settings', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ default_servings: String(this.settingsForm.default_servings) }),
  });
  if (!res.ok) {
    this.showToast('Invalid settings value', 'error');
    return;
  }
  this.settings = await res.json();
  this.modal = null;
  this.showToast('Settings saved');
},
```

### `defaultServings()`

A pure getter that parses the stored string:

```js
defaultServings() {
  const v = parseInt(this.settings.default_servings, 10);
  return (v > 0) ? v : 2;
},
```

### `recipeScaleServings(recipe)`

Returns the current stepper value for a given recipe, falling back to the recipe's own servings (or the global default if the recipe has 0):

```js
recipeScaleServings(recipe) {
  if (this.recipeServings[recipe.id] != null) return this.recipeServings[recipe.id];
  return (recipe.servings > 0) ? recipe.servings : this.defaultServings();
},
```

### `setRecipeServings(recipe, value)`

Writes the stepper value, clamping to a minimum of 1:

```js
setRecipeServings(recipe, value) {
  const v = parseInt(value, 10);
  this.recipeServings[recipe.id] = (v > 0) ? v : 1;
},
```

---

## `addRecipeToShoppingList(recipe)` Change

**Current:**
```js
async addRecipeToShoppingList(recipe) {
  const res = await fetch(`/api/recipes/${recipe.id}/add-to-shopping-list`, { method: 'POST' });
  ...
}
```

**New:**
```js
async addRecipeToShoppingList(recipe) {
  const servings = this.recipeScaleServings(recipe);
  const res = await fetch(`/api/recipes/${recipe.id}/add-to-shopping-list`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ servings }),
  });
  const data = await res.json();
  this.modal = null;
  this.showToast(`Added ${data.added} missing ingredient(s) to shopping list`);
  await this.fetchShoppingList();
},
```

---

## New State: `settingsForm`

Add alongside other form objects:

```js
settingsForm: { default_servings: 2 },
```

### `openSettings()`

```js
openSettings() {
  this.settingsForm = { default_servings: this.defaultServings() };
  this.modal = 'settings';
},
```

---

## UI Changes

### 1. Settings button in the header

Inside the `<header>` element, add a gear button to the right of the existing title/tab indicator:

```html
<button @click="openSettings()" class="text-white/80 text-xl" title="Settings">⚙️</button>
```

### 2. Settings modal

Add a new modal alongside the existing modals:

```html
<!-- Settings Modal -->
<div x-show="modal === 'settings'" class="fixed inset-0 bg-black/50 z-20 flex items-end justify-center sm:items-center">
  <div class="bg-white rounded-t-2xl sm:rounded-2xl w-full max-w-lg p-6 space-y-4">
    <h2 class="text-lg font-bold">Settings</h2>
    <div class="flex gap-2 items-center">
      <label class="text-sm text-gray-600 whitespace-nowrap">Default servings:</label>
      <input x-model.number="settingsForm.default_servings"
             type="number" min="1"
             class="flex-1 border rounded-lg px-3 py-2 text-sm" />
    </div>
    <div class="flex gap-2 pt-2">
      <button @click="modal = null" class="flex-1 border rounded-lg py-2 text-sm">Cancel</button>
      <button @click="saveSettings()" class="flex-1 bg-green-600 text-white rounded-lg py-2 text-sm font-medium">Save</button>
    </div>
  </div>
</div>
```

### 3. Serving count stepper on each recipe card

Inside the `<template x-for="recipe in recipes">` card, add a servings stepper row between the tags line and the action buttons. The stepper goes inside the card's inner `<div>` (the flex container that already holds the recipe name, description, and tags):

```html
<div class="flex items-center gap-2 mt-2">
  <span class="text-xs text-gray-500">Servings:</span>
  <button @click="setRecipeServings(recipe, recipeScaleServings(recipe) - 1)"
          class="w-6 h-6 rounded border text-gray-600 text-sm font-bold flex items-center justify-center">−</button>
  <input type="number" min="1"
         :value="recipeScaleServings(recipe)"
         @change="setRecipeServings(recipe, $event.target.value)"
         class="w-12 border rounded text-center text-sm py-0.5" />
  <button @click="setRecipeServings(recipe, recipeScaleServings(recipe) + 1)"
          class="w-6 h-6 rounded border text-gray-600 text-sm font-bold flex items-center justify-center">+</button>
  <span class="text-xs text-gray-400"
        x-text="recipe.servings > 0 ? '(recipe: ' + recipe.servings + ')' : '(default: ' + defaultServings() + ')'"></span>
</div>
```

The "Add to shopping list" button in the View Recipe modal continues to call `addRecipeToShoppingList(selectedRecipe)`. `selectedRecipe` is the full recipe object, so `recipeScaleServings(selectedRecipe)` reads the stepper value set from the card list (or falls back to the recipe default).

### 4. Tailwind safelist

The safelist comment at the bottom of the HTML file (currently `<div class="hidden bg-green-600 bg-red-600">`) does not need new classes — all classes used by this feature (`border`, `rounded`, `text-sm`, `text-gray-600`, etc.) are already in the CDN build. No safelist changes required.

---

## Scaling Math

```
scale = selected_servings / recipe_servings
scaled_quantity = ingredient.quantity * scale
shortfall = scaled_quantity - inventory_quantity_on_hand   (clamped to 0 if negative)
```

Where:
- `selected_servings` = value from `recipeServings[recipe.id]` (or fallback via `recipeScaleServings()`)
- `recipe_servings` = `recipe.servings` (guaranteed >= 1 by schema DEFAULT and the handler's `if recipeServings == 0 { recipeServings = 1 }` guard)
- The backend already performs this computation; the frontend is only responsible for sending `servings` in the request body.

When `selected_servings == recipe_servings`, `scale == 1.0` and behaviour is identical to today.

---

## Data Flow

1. On `init()`, `fetchSettings()` populates `settings.default_servings`.
2. When the Recipes tab loads, `fetchRecipes()` returns each recipe including its `servings` field.
3. On the recipe card, `recipeScaleServings(recipe)` provides the stepper's initial value:
   - If `recipeServings[recipe.id]` has been set this session, use it.
   - Else use `recipe.servings` (guaranteed >= 1).
4. The user optionally adjusts the stepper. `setRecipeServings(recipe, value)` stores the value in `recipeServings`.
5. The user opens the View Recipe modal and clicks "Add to shopping list".
6. `addRecipeToShoppingList(selectedRecipe)` reads the current stepper value via `recipeScaleServings(selectedRecipe)` and POSTs `{ servings: N }` to `/api/recipes/{id}/add-to-shopping-list`.
7. The Go handler decodes `body.Servings`, computes `scale = body.Servings / recipe.servings`, multiplies each ingredient's quantity, then computes and inserts shortfalls as today.
8. The frontend shows the existing toast confirming how many items were added.

---

## Edge Cases

- **`recipe.servings == 0`:** The handler already guards `if recipeServings == 0 { recipeServings = 1 }` — divide-by-zero is impossible. `recipeScaleServings()` on the frontend also falls back to `defaultServings()` when `recipe.servings <= 0`.
- **`body.Servings <= 0` in the request body:** The handler clamps to `1` before computing the scale. No 400 is returned — this is user-supplied scaling, not a structural error.
- **`settings` table empty on first start:** `INSERT OR IGNORE` seeds `default_servings = '2'` at startup, so `GET /api/settings` always returns at least one key.
- **`default_servings` missing from `settings` object:** `defaultServings()` returns `2` if the parsed value is not a positive integer, providing a safe fallback even if the row is somehow missing.
- **Stepper value resets on reload:** Intentional. `recipeServings` is session-only state; persistent per-recipe preferences are out of scope.
- **View Recipe modal opened without adjusting stepper on the card:** `selectedRecipe` is set to the full recipe object from the `recipes` array. `recipeScaleServings(selectedRecipe)` reads from `recipeServings[selectedRecipe.id]`, which may be unset, in which case it falls back to `selectedRecipe.servings`. This is correct.
- **`PATCH /api/settings` with an unknown key:** accepted and stored. The settings table is open-schema by design.

---

## What Is Not Implemented

- Persistent per-recipe serving count (the stepper is session-only).
- Serving count display in the View Recipe modal's ingredient list (quantities are shown at recipe-canonical scale; the scaled quantities are only computed server-side at shopping list generation time).
- Any settings beyond `default_servings` (the modal has one field, but the backend accepts any key).
- A dedicated settings navigation tab (a header gear button and modal are sufficient for MVP).
- Editing a recipe's canonical `servings` field from the recipe card (the Add Recipe modal already has a servings input; a full edit flow is out of scope for this feature).
