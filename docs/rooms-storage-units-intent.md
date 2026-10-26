# Intent: rooms & storage units (structured locations)

Status: **complete.** Backend in #37, picker and API plumbing in #38.
Captured 2026-10-25 from an
uncommitted local implementation (`handlers/locations.go`, 262 lines +
`handlers/locations_test.go`, 407 lines) that predates main's
products/stock-lots refactor. The old code is preserved on the branch
`legacy/locations-prerefactor` for reference, but it should be **rebuilt against
the new architecture, not merged** — it was written against the pre-split
`inventory` table and the single-file `static/index.html` frontend.

## Problem

`inventory.location` was free text. "Kitchen", "kitchen", "Kitchen counter" and
"counter" were four different places, so items couldn't be grouped reliably,
pickers offered inconsistent options, and there was no way to ask "what's in the
pantry".

## Intent

Replace free-text location with a **two-level hierarchy**: a *room* contains
*storage units*, and an inventory row points at one storage unit.

```
Kitchen (room)
 ├─ Fridge        (storage unit)  ← inventory rows point here
 └─ Pantry        (storage unit)
```

The user-facing label is `Room › Unit` (e.g. `Kitchen › Fridge`), and `Unfiled`
when an item has no storage unit.

## Data model

- `rooms`: `id`, `name NOT NULL UNIQUE`.
- `storage_units`: `id`, `room_id NOT NULL REFERENCES rooms(id) ON DELETE CASCADE`,
  `name NOT NULL`, `UNIQUE(room_id, name)` — the same unit name may repeat in
  different rooms ("Kitchen › Shelf" and "Garage › Shelf" coexist).
- The inventory side gets a nullable `storage_unit_id REFERENCES storage_units(id)`.
  Nullable is deliberate: an item with no known location is valid ("Unfiled").

On the **new** schema this column belongs on whichever table represents a
physical quantity in a place (the stock lot, not the product) — a product is an
abstract item, a lot is "3 cans, in the pantry, expiring June". That is the main
thing to re-decide during the rebuild.

## Deletion policy (the key design decision)

**Refuse to delete a room or storage unit while any inventory row still
references it — 409, not a cascade or a silent null.**

Nulling the reference would lose where the food actually is, with no way to
recover it. The user can move or delete those items first. Error strings:

- unit: `storage unit still has inventory items; move or delete them first`
- room: `room still has inventory items in its storage units; move or delete them first`

A room with *empty* units deletes fine — `ON DELETE CASCADE` takes the units
with it. Only inventory references block.

## Migration / backfill

One-time, idempotent, and it must stay safe to re-run on every startup:

1. Ensure a room named `Unsorted` exists.
2. For each `DISTINCT location` that is non-empty and whose rows have no
   `storage_unit_id`, create a storage unit of that name under `Unsorted`.
3. Point those rows at the new unit.

Only touches rows that still have location text *and* no `storage_unit_id`, so
it is a no-op after the first run. **`inventory.location` is deliberately left
in place — this codebase never drops columns.** The `Unsorted` name is chosen so
it is obvious which places still need filing into real rooms.

Live production data as of capture: 2 rooms (`Unsorted`, `Kitchen`), 6 storage
units, 26 inventory rows carrying `storage_unit_id`. **Any rebuild must keep
these rows resolvable** — this is the one piece that cannot be regenerated.

## API

| Method | Path | Notes |
|---|---|---|
| GET | `/api/rooms` | All rooms, each with `storage_units` nested, ordered by room name then unit name. Rooms with no units return `storage_units: []`, never `null`. |
| POST | `/api/rooms` | `{name}`. 201 with the created room. 409 on duplicate name. |
| PATCH | `/api/rooms/{id}` | `{name}`. 404 if absent, 409 on duplicate. |
| DELETE | `/api/rooms/{id}` | 204, or 409 per deletion policy. |
| POST | `/api/rooms/{room_id}/storage-units` | `{name}`. 404 if the room is absent, 409 on duplicate within that room. |
| PATCH | `/api/storage-units/{id}` | `{name}`. 404 / 409 as above. |
| DELETE | `/api/storage-units/{id}` | 204, or 409 per deletion policy. |

Shared rules: body is `{"name": "..."}`, the name is **trimmed**, empty or
whitespace-only is `400 name is required`, malformed JSON is `400 invalid body`.
Every mutation broadcasts `inventory_updated` on the websocket hub so open
clients refresh.

Inventory read endpoints should expose the location as a **nested
`storage_unit` object carrying its room name** (so a client can render
`Room › Unit` without a second lookup), while item responses also carry the flat
`storage_unit_id` for form binding. The old frontend read both and needed to —
worth keeping that shape.

## Frontend intent

- Fetch the hierarchy **once** and drive every location picker from it
  client-side; don't re-fetch per picker.
- A picker is two selects: room, then unit filtered to that room. Editing an
  existing item preselects the room that owns its current unit.
- Allow creating a storage unit inline from the picker (POST then refresh the
  shared hierarchy) so filing an item doesn't require a settings detour.
- Show `Unfiled` for a null unit rather than hiding the item.

## Behaviour to re-pin with tests

The original suite covered four things; keep these as the contract:

1. **Rooms CRUD** — create, rename, list-with-nested-units, delete; duplicate
   names 409 at both levels; unknown ids 404.
2. **Deletion refused while referenced** — the 409 path for both a unit and a
   room, and that an empty room still deletes.
3. **Legacy location backfill** — legacy text lands under `Unsorted`, rows get
   linked, and a second run changes nothing (idempotence).
4. **Inventory endpoints reflect the storage unit** — the nested
   `storage_unit` (with room name) and flat `storage_unit_id` both appear.

## Rebuild checklist

- [x] Decide product vs stock-lot ownership of `storage_unit_id` — **stock_lots**,
      confirmed by migration 10 putting `location` there.
- [x] Port the schema + the idempotent backfill into main's migration system
      (migration 11, `rooms_and_storage_units`).
- [x] Re-add the handlers in main's style — typed `Room`/`StorageUnit`,
      `WriteServerError` instead of leaking `err.Error()` to the browser.
- [x] Re-pin the behaviours as tests (`handlers/locations_test.go`), plus a new
      ambiguity case the original didn't cover.
- [x] Verify against a copy of production data: all 26 lots keep their links,
      existing units are reused not duplicated, and a second start is a no-op.
- [x] Re-add the location picker to the ES-module frontend
      (`static/js/locations.js`, composed into `app.js`, precached in `sw.js`).
- [x] Accept `storage_unit_id` on `POST`/`PATCH /api/inventory` — without this
      the picker had nothing to save to.

## Frontend

`static/js/locations.js` holds the shared hierarchy, fetched once in `init()`
and composed into the app object like the other modules. The item modal's old
free-text input is now two selects (room, then its units) plus an inline
"new storage unit" field, and `syncPickerRoom()` preselects the room that owns
the item's current unit when the modal opens.

`sw.js` must list the module or the PWA breaks offline — there is a test
(`TestServiceWorkerShell`) that enforces this.

### Still legacy

The grouped inventory view, the location filter and the putaway flow still key
off the free-text `stock_lots.location`, which is why migration 11 keeps that
column and `saveItem` still sends it. Migrating those views onto
`storage_unit_id` is separate work; the two representations coexist for now,
and nothing regressed.

## Note on the 415

New main's `apiguard` requires `Content-Type: application/json` on every
mutating request, DELETEs included, even with no body. Hand-written curl
against these endpoints needs the header or it gets a 415 before reaching the
handler.
