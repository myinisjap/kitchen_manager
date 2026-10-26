# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build -o kitchen_manager

# Run (plain HTTP on :8080 by default; see Running Modes below for TLS/Docker)
./kitchen_manager

# Run all tests
go test ./...

# Run tests for a specific package
go test ./units/
go test ./handlers/

# Run a single test
go test -v -run TestGetInventoryItemByBarcode ./handlers/
```

No Makefile or external build tools. Frontend has no build step (CDN-loaded dependencies).

## Running Modes

**Local dev (no Docker, no auth):**
```bash
SELF_SIGNED_TLS=true go run .
# https://localhost:8443
```

**Local dev (plain HTTP, no auth):**
```bash
go run .
# http://localhost:8080
```

**Production (Docker, behind Traefik):** see the `start-kitchen-manager-traefik` skill
for the full procedure (DB backup, `.env` checks, verification).

Compose is split like `agent-task-editor`: a base file plus a Traefik overlay.
`--env-file .env` is required because `.env` sits at the repo root while the compose
files are in `deploy/`; the host rule reads `TRAEFIK_HOST` and fails loudly if unset.

```bash
cp .env.example .env  # fill in values, incl. TRAEFIK_HOST
docker compose --env-file .env -f deploy/docker-compose.yml \
  -f deploy/docker-compose.traefik.yml up -d
```

**Redeploy production (after code changes):**
```bash
COMPOSE="docker compose --env-file .env -f deploy/docker-compose.yml -f deploy/docker-compose.traefik.yml"
$COMPOSE down && $COMPOSE up -d --build
```

Omit the overlay for a local Docker run with no Traefik route (publishes 127.0.0.1:8080).

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `TRAEFIK_HOST` | — | Public hostname for the Traefik host rule (overlay only; required) |
| `DB_PATH` | `./kitchen.db` | SQLite file path |
| `SELF_SIGNED_TLS` | unset | Set to `true` to serve self-signed HTTPS on :8443 |
| `OAUTH_ENABLED` | unset | Set to `true` to require Google OAuth |
| `OAUTH_ALLOWED_EMAILS` | — | Comma-separated email whitelist |
| `GOOGLE_CLIENT_ID` | — | Google Cloud OAuth client ID |
| `GOOGLE_CLIENT_SECRET` | — | Google Cloud OAuth client secret |
| `SESSION_SECRET` | — | 64-char hex string for cookie signing (`openssl rand -hex 32`) |
| `BASE_URL` | — | Public URL, used for OAuth redirect URI |
| `ANTHROPIC_API_KEY` | — | Required for receipt parsing (Claude vision API) |
| `SPLASH_URL` | — | Optional Splash JS renderer URL for recipe URL import |

## Auth

`internal/auth/` contains the full OAuth flow. When `OAUTH_ENABLED` is unset, the auth middleware is not added and all requests pass through unauthenticated. Sessions are stored in SQLite (`sessions` table) using an HMAC-signed token derived from `SESSION_SECRET` — sessions survive app restarts. Session cookies are managed by `alexedwards/scs`.

## Architecture

Single-binary Go web app with an Alpine.js SPA frontend.

**Layers:**
- `main.go` — router setup, TLS bootstrapping, static file serving
- `handlers/` — HTTP request handlers (one file per domain: inventory, shopping, recipes, calendar, meals, history, receipt, settings, recipe_import); `ws.go` manages the WebSocket hub
- `services/` — business logic that spans multiple DB tables (threshold generation, weekly shopping simulation)
- `units/` — unit enum definitions, dimension validation, and conversion math
- `internal/database/` — SQLite schema creation and migration (runs on startup)
- `internal/auth/` — OAuth flow, session management, auth middleware
- `internal/tlscert/` — self-signed TLS certificate generation
- `deploy/` — Dockerfile, docker-compose.yml (Traefik reverse proxy, not Caddy)
- `static/index.html` — entire frontend as a single file (~40KB)

**Request flow:** Frontend → Handler → Service (if needed) → Raw SQL → SQLite (`kitchen.db`)

## Key Design Decisions

**Units system:** 14 units across 3 dimensions (mass, volume, count). Units must be same-dimension to convert. Count units don't inter-convert. The `preferred_unit` on an inventory item is the canonical unit for display and aggregation.

**Shopping list sources:** Items have a `source` field: `manual`, `threshold`, `recipe`, or `calendar`. `generate-from-thresholds` skips items already on the unchecked list to avoid duplicates.

**Weekly shopping simulation:** `services/calendar.go` simulates inventory depletion day-by-day across the week, aggregating shortfalls. It handles unit conversion to each item's `preferred_unit`.

**TLS:** Self-signed certificates are auto-generated on startup (`tls.go`) with IP SANs so mobile devices on the same network can connect without cert errors. Certs are persisted as `cert.pem`/`key.pem`.

**Testing:** Tests use in-memory SQLite (not mocks). `newTestDB` in `handlers/inventory_test.go` calls the real `database.OpenDB(":memory:")`, so tests run the production schema and migrations (including `SetMaxOpenConns(1)`) rather than a hand-maintained copy; `units/units_test.go` tests conversion logic directly.

**Frontend:** Alpine.js reactive state, no build step. Barcode scanning via ZXing.js (camera API). Autocomplete pulls from `GET /api/inventory/suggestions` (distinct previous names + units).

**Reverse proxy (Traefik):** The app runs at `/kitchen-manager` behind Traefik's `stripprefix` middleware. Auth redirects must use absolute URLs via `BASE_URL` (not relative paths like `/auth/login`) so the browser preserves the path prefix.

**WebSocket real-time updates:** A `Hub` in `handlers/ws.go` broadcasts a JSON event `{"type":"inventory_updated"}` to all connected clients whenever inventory changes. Handlers call `hub.Broadcast(...)` after mutations. The frontend reconnects automatically on disconnect.

**Receipt parsing:** `handlers/receipt.go` accepts an uploaded image, converts it to grayscale (reduces size ~60–70%), and sends it to Claude via `ANTHROPIC_API_KEY` to extract line items. Falls back gracefully if the key is unset.

**Recipe import:** `handlers/recipe_import.go` extracts `schema.org/Recipe` JSON-LD from HTML. Fetching tries a Splash JS renderer (`SPLASH_URL`) first, then falls back to a direct GET with a browser User-Agent. PDF import is also supported.

**Meal history:** Cooking a recipe logs it to `meal_history` and `meal_history_ingredients`, and deducts ingredients from inventory in the same transaction.

## Database Schema Notes

- `inventory.low_threshold` — when `quantity` drops below this, `generate-from-thresholds` adds the shortfall to the shopping list
- `inventory.preferred_unit` — added via migration on startup if the column doesn't exist
- `shopping_list.inventory_id` is nullable (manual items may not link to an inventory item)
- `recipe_ingredients.inventory_id` is nullable (ingredients may not be linked to inventory)
- Recipe deletion is transactional (deletes ingredients first, then recipe)
- `inventory_history` — records every quantity change with `change_type` (add/remove/edit), delta, and optional note; written via `handlers.LogHistory()`
- `inventory_skus` — barcode aliases per inventory item, each with a `quantity_per_scan` multiplier; added via migration on startup
- `meal_history` / `meal_history_ingredients` — immutable log of cooked meals; ingredient quantities recorded at cook time
- `settings` — single key/value table for app-wide config (currently stores notification preferences)
- `rooms` / `storage_units` — two-level location hierarchy (a room contains storage units); unit names are unique per room, not globally, so "Top Shelf" can exist in several rooms. Deleting a room cascades to its units
- `inventory.storage_unit_id` — nullable FK replacing the old free-text `location`; NULL means the item is unfiled. Each physical location is its own inventory row (siblings share `name`+`unit`), so this is a single FK, not a join table
- `inventory.location` — legacy free-text column, unused since the `storage_units` migration. Kept because this codebase never drops columns; the startup backfill copies each distinct non-empty value into a storage unit under an "Unsorted" room, and only touches rows where `storage_unit_id IS NULL`, so it is a no-op on later startups
- API responses embed the location as a nested `storage_unit` object (`{"id","name","room_id","room"}`, or `null`) alongside `storage_unit_id`; the same shape is used by every inventory endpoint. `GET /api/rooms` returns the hierarchy and replaced the old flat `GET /api/inventory/locations` autocomplete
- Room/storage-unit deletion returns 409 while any inventory row still references it, rather than orphaning items
- Handlers inside a transaction must query through the `*sql.Tx`, never the pooled `*sql.DB` — `SetMaxOpenConns(1)` means a pool query while a tx is open deadlocks the server (see `resolveInventoryID`'s `querier` interface)
