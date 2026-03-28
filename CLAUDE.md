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

**Production (Docker):**
```bash
cp .env.example .env  # fill in values
docker compose up -d
```

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `DB_PATH` | `./kitchen.db` | SQLite file path |
| `SELF_SIGNED_TLS` | unset | Set to `true` to serve self-signed HTTPS on :8443 |
| `OAUTH_ENABLED` | unset | Set to `true` to require Google OAuth |
| `OAUTH_ALLOWED_EMAILS` | — | Comma-separated email whitelist |
| `GOOGLE_CLIENT_ID` | — | Google Cloud OAuth client ID |
| `GOOGLE_CLIENT_SECRET` | — | Google Cloud OAuth client secret |
| `SESSION_SECRET` | — | 64-char hex string for cookie signing (`openssl rand -hex 32`) |
| `BASE_URL` | — | Public URL, used for OAuth redirect URI |

## Auth

`auth.go` contains the full OAuth flow. When `OAUTH_ENABLED` is unset, the `authMiddleware` is not added and all requests pass through unauthenticated. Sessions are stored in SQLite (`sessions` table) using an HMAC-signed token derived from `SESSION_SECRET` — sessions survive app restarts. Session cookies are managed by `alexedwards/scs`.

## Architecture

Single-binary Go web app with an Alpine.js SPA frontend.

**Layers:**
- `main.go` — router setup, TLS bootstrapping, static file serving
- `handlers/` — HTTP request handlers (one file per domain: inventory, shopping, recipes, calendar)
- `services/` — business logic that spans multiple DB tables (threshold generation, weekly shopping simulation)
- `units/` — unit enum definitions, dimension validation, and conversion math
- `models.go` — shared structs used across handlers and services
- `db.go` — SQLite schema creation and migration (runs on startup)
- `static/index.html` — entire frontend as a single file (~40KB)

**Request flow:** Frontend → Handler → Service (if needed) → Raw SQL → SQLite (`kitchen.db`)

## Key Design Decisions

**Units system:** 14 units across 3 dimensions (mass, volume, count). Units must be same-dimension to convert. Count units don't inter-convert. The `preferred_unit` on an inventory item is the canonical unit for display and aggregation.

**Shopping list sources:** Items have a `source` field: `manual`, `threshold`, `recipe`, or `calendar`. `generate-from-thresholds` skips items already on the unchecked list to avoid duplicates.

**Weekly shopping simulation:** `services/calendar.go` simulates inventory depletion day-by-day across the week, aggregating shortfalls. It handles unit conversion to each item's `preferred_unit`.

**TLS:** Self-signed certificates are auto-generated on startup (`tls.go`) with IP SANs so mobile devices on the same network can connect without cert errors. Certs are persisted as `cert.pem`/`key.pem`.

**Testing:** Tests use in-memory SQLite (not mocks). `handlers/inventory_test.go` builds the schema in-process; `units/units_test.go` tests conversion logic directly.

**Frontend:** Alpine.js reactive state, no build step. Barcode scanning via ZXing.js (camera API). Autocomplete pulls from `GET /api/inventory/suggestions` (distinct previous names + units).

## Database Schema Notes

- `inventory.low_threshold` — when `quantity` drops below this, `generate-from-thresholds` adds the shortfall to the shopping list
- `inventory.preferred_unit` — added via migration on startup if the column doesn't exist
- `shopping_list.inventory_id` is nullable (manual items may not link to an inventory item)
- `recipe_ingredients.inventory_id` is nullable (ingredients may not be linked to inventory)
- Recipe deletion is transactional (deletes ingredients first, then recipe)
