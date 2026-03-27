# Unit Enum & Conversion Design

**Date:** 2026-03-27
**Status:** Approved

## Overview

Replace free-text `unit` strings throughout the app with a validated enum and a conversion layer that allows recipe ingredients and pantry items to use compatible but different units (e.g. recipe says "0.5 kg", pantry tracks "g").

## Unit Enum

All valid unit values, defined as a `Unit` type in a new `units` package:

| Dimension | Units |
|-----------|-------|
| mass      | `g`, `kg`, `oz`, `lb` |
| volume    | `ml`, `L`, `cup`, `tbsp`, `tsp` |
| count     | `piece`, `clove`, `can`, `jar`, `bunch` |

Conversion is only possible within a dimension. Cross-dimension comparisons (e.g. recipe uses `clove`, pantry tracks `bunch`) are left as-is with no conversion attempted.

## Package: `units/units.go`

Exports:

- `type Unit string` — the enum type
- `IsValid(u Unit) bool` — rejects unknown strings at API boundaries
- `Convert(qty float64, from, to Unit) (float64, error)` — returns error if dimensions differ
- `BaseDimension(u Unit) string` — returns `"mass"`, `"volume"`, or `"count"`
- `BaseUnit(u Unit) Unit` — normalized base unit (`g` for mass, `ml` for volume, unchanged for count)

## Data Model Changes

One new column on `inventory`:

```sql
preferred_unit TEXT NOT NULL DEFAULT ''
```

When `preferred_unit` is empty, the item's stored `unit` is used as the preferred unit (preserves backward compatibility with existing data). No changes to `recipe_ingredients` or `shopping_list` schema — conversion happens at query time.

**Migration:** At startup, `createSchema()` checks `PRAGMA table_info(inventory)` for the `preferred_unit` column and runs `ALTER TABLE inventory ADD COLUMN preferred_unit TEXT NOT NULL DEFAULT ''` only if it is absent. SQLite does not support `ADD COLUMN IF NOT EXISTS`.

## Conversion in Business Logic

### Recipe availability check (`handlers/recipes.go` — `recipeIsAvailable`)

When comparing a recipe ingredient's quantity+unit against pantry quantity+unit:
1. If both units are in the same dimension, convert the ingredient quantity to the inventory item's unit before comparing.
2. If units are in different dimensions (not convertible), compare as-is (treat as no match).

### Weekly shopping generation (`services/calendar.go` — `GenerateWeeklyShopping`)

When computing shortfall per ingredient:
1. If the ingredient has an `inventory_id`, convert the ingredient quantity to the inventory item's `preferred_unit` (falling back to stored `unit` if `preferred_unit` is empty).
2. The resulting `ShoppingNeed.Unit` uses the inventory item's preferred unit.
3. For unlinked ingredients (no `inventory_id`), no conversion — use the unit as written in the recipe.

Shopping list rows are inserted with the already-converted `quantity_needed` and preferred unit, so no further runtime conversion is needed for display.

## API Changes

### Validation

All endpoints that accept a `unit` field now validate against the enum. Invalid unit strings return `400 Bad Request` with a body listing valid values. Affected endpoints:

- `POST /api/inventory/` and `PATCH /api/inventory/{id}`
- `POST /api/shopping/` and `PATCH /api/shopping/{id}`
- `POST /api/recipes/` (validates each ingredient's unit)

`preferred_unit` on inventory create/update:
- Optional field
- Must pass enum validation if provided
- Must be the same dimension as `unit` if both are set; otherwise returns `400`

### New Endpoint

`GET /api/units` — returns all valid units grouped by dimension:

```json
{
  "mass":   ["g", "kg", "oz", "lb"],
  "volume": ["ml", "L", "cup", "tbsp", "tsp"],
  "count":  ["piece", "clove", "can", "jar", "bunch"]
}
```

Used by the frontend to populate dropdowns without hardcoding.

## Frontend Changes

- Unit free-text inputs in all three forms (add/edit inventory, add shopping item, add recipe ingredient) become `<select>` dropdowns.
- Dropdown options are populated from `GET /api/units` on app load, grouped by dimension using `<optgroup>`.
- The inventory add/edit modal gains an optional "Preferred Unit" `<select>` that filters to units in the same dimension as the selected unit.
- No changes to quantity display — the unit string shown is now always a valid enum value.

## Testing

### New: `units/units_test.go`

- All valid unit round-trips (g→kg→g, ml→L→ml, cup→tbsp→cup, etc.)
- Cross-dimension conversion returns an error
- `IsValid` rejects unknown strings

### Updated: `handlers/inventory_test.go`

- Existing tests already use valid enum values (`"g"`, `"jar"`) — minimal changes expected.
- New test: `POST /api/inventory/` with invalid unit string returns 400.
- New test: `POST /api/inventory/` with `preferred_unit` in different dimension from `unit` returns 400.
