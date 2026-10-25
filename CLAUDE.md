# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build -o kitchen_manager

# Run (plain HTTP on :8080 by default; see Running Modes below for TLS/Docker)
./kitchen_manager

# Run all tests (CI uses -race)
go test -race ./...

# Vulnerability scan (CI runs this too)
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...

# Run tests for a specific package
go test ./units/
go test ./handlers/

# Run a single test
go test -v -run TestGetInventoryItemByBarcode ./handlers/
```

No Makefile. Frontend has no runtime build step: Alpine and ZXing are vendored in `static/vendor/` (see its README) and Tailwind CSS is pre-built and committed as `static/tailwind.css`. After changing classes in `static/index.html` or `static/app.js`, rebuild: `npx tailwindcss@3.4.19 -c tailwind.config.js -i web/tailwind.input.css -o static/tailwind.css --minify`.

Tests must build their database with `testutil.NewDB(t)` (`internal/testutil`, real schema via `database.OpenDB(":memory:")`), never a hand-written schema.

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
docker compose --env-file .env -f deploy/docker-compose.yml up -d
```

**Redeploy production (after code changes):**
```bash
docker compose --env-file .env -f deploy/docker-compose.yml down && docker compose --env-file .env -f deploy/docker-compose.yml up -d --build
```

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `DB_PATH` | `./kitchen.db` | SQLite file path |
| `SELF_SIGNED_TLS` | unset | Set to `true` to serve self-signed HTTPS on :8443 |
| `OAUTH_ENABLED` | unset | Set to `true` to require Google OAuth |
| `TRUST_FORWARDED_USER` | unset | Set to `true` only when the app is reachable solely through Traefik forward-auth (which must forward `X-Forwarded-User`); the header then becomes the user identity (history `changed_by`, `/api/me`). Leave unset otherwise: the header is spoofable. OAuth session email takes precedence. |
| `OAUTH_ALLOWED_EMAILS` | — | Comma-separated email whitelist |
| `GOOGLE_CLIENT_ID` | — | Google Cloud OAuth client ID |
| `GOOGLE_CLIENT_SECRET` | — | Google Cloud OAuth client secret |
| `SESSION_SECRET` | — | 64-char hex string for cookie signing (`openssl rand -hex 32`) |
| `APP_HOST` | — | Public hostname for the Traefik router in `deploy/docker-compose.yml` |
| `BASE_URL` | — | Public URL incl. any path prefix (e.g. `https://host/kitchen-manager`); used for OAuth and auth redirects |

## Auth

`internal/auth/` contains the full OAuth flow. When `OAUTH_ENABLED` is unset, the auth middleware is not added and all requests pass through unauthenticated. Sessions are stored in SQLite (`sessions` table) using an HMAC-signed token derived from `SESSION_SECRET` — sessions survive app restarts. Session cookies are managed by `alexedwards/scs`.

## Architecture

Single-binary Go web app with an Alpine.js SPA frontend.

**Layers:**
- `main.go` — router setup, TLS bootstrapping, static file serving, explicit `http.Server` timeouts, graceful shutdown (SIGINT/SIGTERM, closes DB)
- `handlers/` — HTTP request handlers (one file per domain: inventory, shopping, recipes, calendar)
- `services/` — business logic that spans multiple DB tables (threshold generation, weekly shopping simulation)
- `units/` — unit enum definitions, dimension validation, and conversion math
- `internal/database/` — SQLite setup + numbered, append-only migrations tracked by `PRAGMA user_version` (run on startup). `foreign_keys`, `busy_timeout` and WAL are set in the DSN so they apply to every connection.
- `internal/auth/` — OAuth flow, session management, auth middleware
- `internal/tlscert/` — self-signed TLS certificate generation (key.pem is written 0600)

**CSRF / origin guard:** `handlers.APIGuard` (wired in main.go) requires `Content-Type: application/json` on POST/PUT/PATCH/DELETE under `/api/` (multipart only for `/api/receipt/parse` and `/api/recipes/import/pdf`) and rejects a cross-origin `Origin` with 403. The WS upgrader uses the same `SameOrigin` check (host match vs request Host or BASE_URL host). New frontend mutating fetches must send the JSON header. `PATCH /api/settings` only accepts keys in `allowedSettingKeys`; add new settings there.
- `deploy/` — Dockerfile, docker-compose.yml (Traefik reverse proxy, not Caddy)
  Do not add a headless-browser renderer (e.g. Splash) to the compose network; it was removed for security (it could reach app:8080 bypassing forward-auth). Recipe URL import is a direct SSRF-safe fetch (`handlers/safefetch.go`).
- `static/index.html` — frontend markup; logic in `static/app.js`, dark-mode bootstrap in `static/theme-init.js`, styles in generated `static/tailwind.css`, third-party JS in `static/vendor/`. No inline scripts/styles (CSP)

**Request flow:** Frontend → Handler → Service (if needed) → Raw SQL → SQLite (`kitchen.db`)

## Key Design Decisions

- **Servings scaling:** every ingredient quantity is scaled by `units.ServingsScale(selected, recipe.servings)` (recipe.servings 0→1) when cooking, adding to the shopping list (default = recipe servings) and generating weekly shopping. Calendar entries default to the recipe's servings. The frontend mirrors the math in `scaledQty`; `settings.default_servings` seeds new recipes and is the stepper fallback.

**Request limits & timeouts:** `ReadJSON` caps JSON bodies at 1 MB. Receipt uploads (20 MB) and PDFs (10 MB) use `http.MaxBytesReader` (413 when exceeded). Receipt images go through `image.DecodeConfig` first and are rejected above 50 MP (413). PDF extraction is bounded by `pdfParseTimeout` plus a concurrency semaphore (`handlers/limits.go`). `main.go` sets Read/ReadHeader/Write/Idle timeouts; WriteTimeout must stay above the 60 s Claude call.

**Units system:** 14 units across 3 dimensions (mass, volume, count). Units must be same-dimension to convert. Count units don't inter-convert. The `preferred_unit` on an inventory item is the canonical unit for display and aggregation.

**Stock deductions:** `POST /api/inventory/{id}/deduct` (remove-stock scans, deduct modal with `single_lot`) and cooking (`POST /api/meals`) go through `deductStock` in `handlers/deduct.go`. It converts the requested unit to `preferred_unit` (or `unit`), cascades expiry-first across lots, retires emptied lots, and returns an incompatible-units error for cross-dimension deductions (e.g. cups vs grams); cooking skips that ingredient and adds a `warnings` entry to the 201 response. `PATCH /api/inventory/{id}` edits only that lot (typed fields, one UPDATE in one tx, wrong types → 400; quantity 0 retires the lot via `retireEmptyLots`).

**Stock additions:** `POST /api/inventory/` (merge-or-insert, incl. SKU-alias merge), `POST /api/skus/item/{id}` and `merge-into` each run in a single transaction and add via `addStock` (`handlers/stock.go`, `quantity = quantity + ?`), never a quantity computed in Go. Because `OpenDB` uses `SetMaxOpenConns(1)` (kept for single-writer and `:memory:` reasons, not for FKs), a handler must not touch `db` while its `tx` is open (it deadlocks) — pass `tx` to `LogHistory` and read back via `tx`. Receipt import posts lines sequentially.

**Putting away shopping:** `POST /api/shopping/{id}/bought` and `POST /api/shopping/put-away` (all checked rows; per-row overrides by id, rows that fail unit/validation checks are skipped and reported) live in `handlers/putaway.go`, one transaction, stock added via `addStock`. The product is the linked `inventory_id` (or override), else an existing product matching the normalised name, else a new product. Quantity converts into the product's effective unit (`preferred_unit`, else `unit`); cross-dimension units give 400 and change nothing. Lot rule: same name+unit+location+expiry, else reuse a zero-quantity lot, else a new lot (inherits par). The shopping row is deleted (a second call is 404). History source `shopping_bought`; broadcasts `inventory_updated` + `shopping_updated`.

**Shopping list sources:** Items have a `source` field: `manual`, `threshold`, `recipe`, or `calendar`. `generate-from-thresholds` skips products that already have an unchecked list row (any lot of the same name) to avoid duplicates.

**Weekly shopping simulation:** `services/calendar.go` simulates inventory depletion day-by-day across the week, aggregating shortfalls. It handles unit conversion to each item's `preferred_unit`.

**TLS:** Self-signed certificates are auto-generated on startup (`tls.go`) with IP SANs so mobile devices on the same network can connect without cert errors. Certs are persisted as `cert.pem`/`key.pem`.

**Testing:** Tests use in-memory SQLite (not mocks). `handlers/inventory_test.go` builds the schema in-process; `units/units_test.go` tests conversion logic directly.

**Frontend:** Alpine.js reactive state, no build step. Barcode scanning via ZXing.js (camera API). Autocomplete pulls from `GET /api/inventory/suggestions` (distinct previous names + units).

**In-app OAuth (`OAUTH_ENABLED`):** optional second layer behind Traefik forward-auth. Unauthenticated HTML requests redirect to `BASE_URL/auth/login`; `/api/*` and `/ws` get 401 JSON; session token is renewed on login.

**Reverse proxy (Traefik):** The app runs at `/kitchen-manager` behind Traefik's `stripprefix` middleware. Auth redirects must use absolute URLs via `BASE_URL` (not relative paths like `/auth/login`) so the browser preserves the path prefix.

## Database Schema Notes

- `inventory.low_threshold` — par level per product (name): kept identical across all lots; `generate-from-thresholds` sums all lots of a name converted to the product unit (preferred_unit, else first lot's unit) and, when total < par, adds `par − total`. 0 = unset (no rule).
- Migrations: append a new entry to `migrations` in `internal/database/db.go`; never edit or reorder old ones. For FK/constraint changes use `disableFK: true` plus a table rebuild. An older binary refuses a newer DB.
- `inventory_id` FKs on `shopping_list`, `recipe_ingredients`, `meal_history_ingredients` are `ON DELETE SET NULL`.
- `shopping_list.inventory_id` is nullable (manual items may not link to an inventory item)
- `recipe_ingredients.inventory_id` is nullable (ingredients may not be linked to inventory)
- Recipes have an `archived` flag (startup migration); archived recipes are hidden from `GET /api/recipes/` unless `?include_archived=true`. `POST /api/recipes/{id}/archive|unarchive` toggle it and touch nothing else.
- Recipe hard delete is transactional: nulls `meal_history.recipe_id` and `inventory_history.recipe_id` (history and `recipe_name` kept), deletes calendar entries, ingredients, then the recipe. `meal_history.recipe_id` is nullable (rebuild migration for old DBs).

**Security headers / CSP:** `securityHeaders` middleware in `main.go` sets CSP, `X-Content-Type-Options`, `Referrer-Policy`, `X-Frame-Options` on all responses. CSP allows `'unsafe-eval'` because the standard Alpine build needs it (the CSP build can't run the inline expressions in index.html). Any new external origin must be added to `contentSecurityPolicy`; don't add inline scripts/styles or CDN tags.

- `meal_history.cooked_at` is always stored as RFC3339 UTC (`2006-01-02T15:04:05Z`); POST normalizes client offsets and a startup migration normalizes legacy rows. `/api/meals/stats` takes `tz` (IANA, preferred) or `tz_offset` (minutes east of UTC), computes week/month boundaries in that zone, and queries in UTC.

## Deploy notes

- Container runs as non-root UID 10001; base images are pinned by digest (bump the digest with the tag).
- `/healthz` is auth-exempt: served outside auth/session middleware via `withHealthz` in main.go; used by the Dockerfile HEALTHCHECK.
