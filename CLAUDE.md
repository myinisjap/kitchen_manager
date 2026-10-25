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

No Makefile. Frontend has no runtime build step: Alpine and ZXing are vendored in `static/vendor/` (see its README) and Tailwind CSS is pre-built and committed as `static/tailwind.css`. After changing classes in `static/index.html`, `static/app.js` or `static/js/*.js`, rebuild: `npx tailwindcss@3.4.19 -c tailwind.config.js -i web/tailwind.input.css -o static/tailwind.css --minify`.

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
| `ANTHROPIC_API_KEY` | — | Anthropic API key for receipt scanning; receipt import errors when unset |
| `ANTHROPIC_MODEL` | `claude-sonnet-5-5` | Claude model used to parse receipts |
| `BASE_URL` | — | Public URL incl. any path prefix (e.g. `https://host/kitchen-manager`); used for OAuth and auth redirects |

## Auth

`internal/auth/` contains the full OAuth flow. When `OAUTH_ENABLED` is unset, the auth middleware is not added and all requests pass through unauthenticated. Sessions are stored in SQLite (`sessions` table) using an HMAC-signed token derived from `SESSION_SECRET` — sessions survive app restarts. Session cookies are managed by `alexedwards/scs`.

## Architecture

Single-binary Go web app with an Alpine.js SPA frontend.

**Layers:**
- `main.go` — router setup, TLS bootstrapping, static file serving, explicit `http.Server` timeouts, graceful shutdown (SIGINT/SIGTERM, closes DB)
- `handlers/` — HTTP request handlers (one file per domain: inventory, products, shopping, recipes, calendar)
- `services/` — business logic that spans multiple DB tables (threshold generation, weekly shopping simulation)
- `units/` — unit enum definitions, dimension validation, and conversion math
- `internal/database/` — SQLite setup + numbered, append-only migrations tracked by `PRAGMA user_version` (run on startup). `foreign_keys`, `busy_timeout` and WAL are set in the DSN so they apply to every connection.
- `internal/auth/` — OAuth flow, session management, auth middleware
- `internal/tlscert/` — self-signed TLS certificate generation (key.pem is written 0600)

**CSRF / origin guard:** `handlers.APIGuard` (wired in main.go) requires `Content-Type: application/json` on POST/PUT/PATCH/DELETE under `/api/` (multipart only for `/api/receipt/parse` and `/api/recipes/import/pdf`) and rejects a cross-origin `Origin` with 403. The WS upgrader uses the same `SameOrigin` check (host match vs request Host or BASE_URL host). New frontend mutating fetches must send the JSON header. `PATCH /api/settings` only accepts keys in `allowedSettingKeys`; add new settings there.

**Use-it-up:** `GET /api/recipes/use-it-up` (`services/useitup.go`) ranks non-archived recipes by distinct expiring products (quantity>0, expiry ≤ today+`settings.expiry_window_days`, 1..365, default 7, `?days=` overrides) matched by ingredient name or linked lot name, case-insensitively; `/api/inventory/expiring` uses the same window.
- `deploy/` — Dockerfile, docker-compose.yml (Traefik reverse proxy, not Caddy)
  Do not add a headless-browser renderer (e.g. Splash) to the compose network; it was removed for security (it could reach app:8080 bypassing forward-auth). Recipe URL import is a direct SSRF-safe fetch (`handlers/safefetch.go`).
- `static/index.html` — frontend markup; logic is native ES modules (no bundler): `static/app.js` is the entry (core state, init, tabs, WebSocket; composes the parts and sets `window.app`) and `static/js/{api,inventory,scanner,shopping,recipes,calendar,meals}.js` each export a factory returning state+methods. Parts are merged by `compose` (copies property descriptors so getters survive; duplicate keys throw). Don't use object spread to merge, and add new modules to the SW `SHELL`. Dark-mode bootstrap in `static/theme-init.js`, styles in generated `static/tailwind.css`, third-party JS in `static/vendor/`. No inline scripts/styles (CSP)

**Request flow:** Frontend → Handler → Service (if needed) → Raw SQL → SQLite (`kitchen.db`)

## Key Design Decisions

- **Servings scaling:** every ingredient quantity is scaled by `units.ServingsScale(selected, recipe.servings)` (recipe.servings 0→1) when cooking, adding to the shopping list (default = recipe servings) and generating weekly shopping. Calendar entries default to the recipe's servings. The frontend mirrors the math in `scaledQty`; `settings.default_servings` seeds new recipes and is the stepper fallback.

**Request limits & timeouts:** `ReadJSON` caps JSON bodies at 1 MB. Receipt uploads (20 MB) and PDFs (10 MB) use `http.MaxBytesReader` (413 when exceeded). Receipt images go through `image.DecodeConfig` first and are rejected above 50 MP (413). PDF extraction is bounded by `pdfParseTimeout` plus a concurrency semaphore (`handlers/limits.go`). `main.go` sets Read/ReadHeader/Write/Idle timeouts; WriteTimeout must stay above the 90 s Claude call.

**Units system:** 14 units across 3 dimensions (mass, volume, count). Units must be same-dimension to convert. Count units don't inter-convert. The product's *effective unit* (`preferred_unit`, else `unit`) is the canonical unit for display, totals, par and deductions. The unit list for `GET /api/units` and the "valid units" error messages comes from `units.All()` / `ByDimension()` / `ValidList()`; add new units to `units/units.go` only.

**Products and stock lots:** `products` (name unique case-insensitively, `unit`, `preferred_unit`, `par_level`, primary `barcode`, `quantity_per_scan`) hold what the food *is*; `stock_lots` (quantity, own `unit`, location, expiry, unit cost) hold what is on the shelf; `product_skus` are extra barcode aliases of a product. Recipes, the shopping list and meal history reference a **product** (`product_id`, `ON DELETE SET NULL`), never a lot, so availability, shortfall, par and weekly shopping use `productStock` / `services.ProductLevels` (sum of all lots converted to the effective unit; lots that cannot convert are kept but left out of totals and flagged `unit_mismatch` in the grouped view). `/api/inventory/*` ids are still **lot** ids and lot JSON keeps its old keys (`low_threshold` = product par, `barcode` = product barcode) plus `product_id`; the SKU endpoints take a lot id and act on its product. Recipe/shopping POST/PUT still accept a legacy `inventory_id` (a lot id) and map it to the product. All product lookups by name go through `findProductByName` / `getOrCreateProduct` (`handlers/products.go`); there is no `resolveInventoryID`. `GET /api/inventory/grouped` also lists products with no lots (`out_of_stock: true`, `locations: []`, `recommended_location_id: 0`, `total_quantity: 0`; left out when `?location=` is set); every group carries the product `barcode`. Lot reads go through `handlers/lotstore.go` (typed `Lot` struct, `getLot`/`listLots`/`expiringLots`/`lotsOfProduct`/`productStub`, `getLotCore`, `deleteLot`); API structs declare fields in alphabetical json order so output stays byte-identical to the old sorted `map[string]any` encoding.

**Product endpoints** (`handlers/products_api.go`, `RegisterProducts`; ids are **product** ids): `GET/PATCH/DELETE /api/products/{id}` (PATCH takes name, preferred_unit, par_level, barcode, quantity_per_scan; 409 on a name/barcode clash, 400 for a preferred unit of another dimension; DELETE removes the product and its lots, logging a `delete` history row per lot), `POST /api/products/{id}/merge-into/{target_id}` (uses `mergeProductInto`; lots keep their own units), and `GET /api/history/product/{id}` (history across all lots; empty list for unknown ids since history outlives products). History rows carry `product_id`.

**Stock deductions:** `POST /api/inventory/{id}/deduct` (remove-stock scans, deduct modal with `single_lot`) and cooking (`POST /api/meals`, by `product_id`) go through `deductStock` in `handlers/deduct.go`. It converts the requested unit to the product's effective unit, cascades expiry-first across **all lots of the product** (converting between lot units; unconvertible lots are skipped), deletes emptied lots (the product, its par and its recipe links stay, so it still reaches the shopping list), costs the deduction from each consumed lot's `unit_cost_cents`, and returns an incompatible-units error for cross-dimension deductions (e.g. cups vs grams); cooking skips that ingredient and adds a `warnings` entry to the 201 response. `PATCH /api/inventory/{id}` edits only that lot (typed fields, one UPDATE in one tx, wrong types → 400; quantity 0 deletes the lot via `retireEmptyLots`; lot fields update the lot, product fields (par, preferred unit, barcode, qty per scan) update its product; renaming a lot moves it to the product with that name, renames the product if it is the only lot, else creates a new product). `DELETE /api/inventory/{id}` removes the lot, and the product too when it was the last lot. A barcode lookup for a product with no lots returns a lot-shaped stub with `id: 0`; the SPA treats that as "add a new lot"). Deduct and `PATCH` return the same id-0 stub (plus `lot_id`) when the product runs out; the SPA treats `id: 0` as "out of stock / add a new lot" everywhere (barcode scan, grocery, remove-stock, deduct).

**Stock additions:** `POST /api/inventory/` (merge-or-insert, incl. SKU-alias merge), `POST /api/skus/item/{id}` and `merge-into` each run in a single transaction and add via `addStock` (`handlers/stock.go`, `quantity = quantity + ?`), never a quantity computed in Go. Because `OpenDB` uses `SetMaxOpenConns(1)` (kept for single-writer and `:memory:` reasons, not for FKs), a handler must not touch `db` while its `tx` is open (it deadlocks) — pass `tx` to `LogHistory` and read back via `tx`. Receipt import posts lines sequentially.

**Putting away shopping:** `POST /api/shopping/{id}/bought` and `POST /api/shopping/put-away` (all checked rows; per-row overrides by id, rows that fail unit/validation checks are skipped and reported) live in `handlers/putaway.go`, one transaction, stock added via `addStock`. The product is the row's linked `product_id` (or an override `product_id` / legacy `inventory_id` lot id), else an existing product matching the normalised name, else a new product. Quantity converts into the product's effective unit; cross-dimension units give 400 and change nothing. Lot rule: same product+unit+location+expiry, else a new lot (empty lots no longer exist). The shopping row is deleted (a second call is 404). History source `shopping_bought`; broadcasts `inventory_updated` + `shopping_updated`.

**Shopping list sources:** Items have a `source` field: `manual`, `threshold`, `recipe`, or `calendar`. `generate-from-thresholds` skips products that already have an unchecked list row (linked to the product, or a threshold row of the same name) to avoid duplicates. Weekly and recipe additions go through `services.MergeIntoShoppingList`: the amount merges into the lowest-id *unchecked* row for the same product or normalised name with a compatible unit (converted into the row's unit); otherwise a new row is inserted. Provenance is stored in `shopping_list_sources` (one row per shopping_id+source+ref, with a display label), returned as `sources` on the shopping API and shown in the UI. Weekly generation is **idempotent per `week_start`** (its previous contribution is replaced; items no longer needed are left alone, rows are never shrunk or deleted); adding a recipe is **additive** (user explicitly asked again, so it doubles the quantity on the same row). Checked rows are never merge targets. Any code deleting shopping rows must delete their `shopping_list_sources` first (FK; use the tx on the single connection).

**Weekly shopping simulation:** `services/calendar.go` compares each recipe need to the product's total stock (all lots, in the product's effective unit) and aggregates shortfalls. Entries already cooked (a linked `meal_history` row exists) are skipped, since their stock was already deducted.

**TLS:** Self-signed certificates are auto-generated on startup (`tls.go`) with IP SANs so mobile devices on the same network can connect without cert errors. Certs are persisted as `cert.pem`/`key.pem`.

**Testing:** Tests use in-memory SQLite (not mocks). tests build the schema via `testutil.NewDB` and add stock with `testutil.AddLot`; `units/units_test.go` tests conversion logic directly.

**Frontend:** Alpine.js reactive state, no build step. Barcode scanning via ZXing.js (camera API). Autocomplete pulls from `GET /api/inventory/suggestions` (distinct previous names + units). Every mutating request (POST/PUT/PATCH/DELETE) must use `apiFetch(path, opts, {errorPrefix, onError})` (defined in `static/js/api.js`). It shows an error toast with the server's `error` message on non-2xx or network failure and returns null, so callers must bail out without updating state. `TestMutatingFetchesUseWrapper` enforces this by scanning `static/app.js` and `static/js/*.js`.

**PWA / offline:** `static/sw.js`, `static/manifest.json` and `static/icons/` make the app installable. The SW caches only the app shell (`SHELL` list, network-first) and `GET api/shopping/`; all other API calls are network only. Bump `CACHE` in `sw.js` when the shell list or caching logic changes, and update `SHELL` when vendor filenames change (`TestServiceWorkerShell` enforces existence, and that `SHELL` lists every `static/js/*.js` module). All PWA URLs are relative or built from `basePath` (app runs at `/` or `/kitchen-manager`). Offline shopping check-offs are queued in localStorage (`km:<basePath>:pendingChecks`) and flushed on `online`, WS reconnect and init, via `apiFetch`'s `onNetworkError` option. Logout clears the SW caches and the queue.

**In-app OAuth (`OAUTH_ENABLED`):** optional second layer behind Traefik forward-auth. Unauthenticated HTML requests redirect to `BASE_URL/auth/login`; `/api/*` and `/ws` get 401 JSON; session token is renewed on login.

**Reverse proxy (Traefik):** The app runs at `/kitchen-manager` behind Traefik's `stripprefix` middleware. Auth redirects must use absolute URLs via `BASE_URL` (not relative paths like `/auth/login`) so the browser preserves the path prefix.

## Database Schema Notes

- `shopping_list_sources` — per-row provenance (shopping_id, source, ref, label, quantity; UNIQUE on shopping_id+source+ref; ON DELETE CASCADE). Used to make weekly generation idempotent and to show where a need came from.
- `products.par_level` — par level per product; `generate-from-thresholds` sums all of the product's lots converted to its effective unit and, when total < par, adds `par − total` (also for products with no lots left). 0 = unset (no rule).
- Migration 9 (`products_and_stock_lots`) split the old one-row-per-lot `inventory` table: product id = lowest lot id of that name (trimmed, ASCII case-insensitive); lot ids are preserved (cached ids and `inventory_history.inventory_id` keep working); par = max of the lots' thresholds; extra barcodes become `product_skus`; incompatible-unit lots of one name stay in one product. `inventory_history` keeps the lot id in `inventory_id` and gains `product_id` (no FK, history outlives products).
- Migrations: append a new entry to `migrations` in `internal/database/db.go`; never edit or reorder old ones. For FK/constraint changes use `disableFK: true` plus a table rebuild. An older binary refuses a newer DB.
- `product_id` FKs on `shopping_list`, `recipe_ingredients`, `meal_history_ingredients` are `ON DELETE SET NULL`; `stock_lots` and `product_skus` cascade from `products`.
- `shopping_list.product_id` is nullable (manual items may not link to a product)
- `recipe_ingredients.product_id` is nullable (ingredients may not be linked to a product)
- Recipes have an `archived` flag (startup migration); archived recipes are hidden from `GET /api/recipes/` unless `?include_archived=true`. `POST /api/recipes/{id}/archive|unarchive` toggle it and touch nothing else.
- Recipe hard delete is transactional: nulls `meal_history.recipe_id` and `inventory_history.recipe_id` (history and `recipe_name` kept), deletes calendar entries, ingredients, then the recipe. `meal_history.recipe_id` is nullable (rebuild migration for old DBs).
- **Calendar ↔ cooking link:** `meal_history.calendar_entry_id` (nullable, added by ALTER after the rebuild migration; unique when set via a partial index). An entry is "cooked" iff a linked meal_history row exists (no separate flag). Deleting a calendar entry or hard-deleting a recipe sets the link to NULL (history kept; handlers do it explicitly). `meal_slot` is a fixed set (breakfast, lunch, dinner, snack); a startup migration lowercases/trims legacy text and maps snacks→snack, supper→dinner, leaving unmatched text untouched (UI shows it under "Other").
- `PATCH /api/calendar/{id}` updates date / meal_slot / servings (any subset; 400 on bad values or empty body, 404 unknown id). `POST /api/meals` accepts `calendar_entry_id`: 404 unknown entry, 400 recipe_id mismatch (recipe_id may be omitted), 409 if already cooked; servings default to the entry's servings.

**Security headers / CSP:** `securityHeaders` middleware in `main.go` sets CSP, `X-Content-Type-Options`, `Referrer-Policy`, `X-Frame-Options` on all responses. CSP allows `'unsafe-eval'` because the standard Alpine build needs it (the CSP build can't run the inline expressions in index.html). Any new external origin must be added to `contentSecurityPolicy`; don't add inline scripts/styles or CDN tags.

- `meal_history.cooked_at` is always stored as RFC3339 UTC (`2006-01-02T15:04:05Z`); POST normalizes client offsets and a startup migration normalizes legacy rows. `/api/meals/stats` takes `tz` (IANA, preferred) or `tz_offset` (minutes east of UTC), computes week/month boundaries in that zone, and queries in UTC.

## Deploy notes

- Container runs as non-root UID 10001; base images are pinned by digest (bump the digest with the tag).
- `/healthz` is auth-exempt: served outside auth/session middleware via `withHealthz` in main.go; used by the Dockerfile HEALTHCHECK.

**Receipt parsing:** `handlers/receipt.go` forces the `record_receipt_items` tool, so output is schema-validated JSON (no fence stripping). `stop_reason == "max_tokens"` → `errReceiptTruncated`. Model comes from `ANTHROPIC_MODEL`. PLU enrichment checks `products.barcode`, then `product_skus` aliases (alias `quantity_per_scan` wins).
