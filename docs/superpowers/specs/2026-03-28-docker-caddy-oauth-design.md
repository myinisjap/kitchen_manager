# Docker + Caddy + Google OAuth Design

**Date:** 2026-03-28

## Goal

Expose kitchen-manager publicly via a TP-Link DDNS hostname with HTTPS (via Caddy + Let's Encrypt) and Google OAuth access control. Keep local development frictionless — no auth, self-signed cert — controlled by env vars.

---

## Architecture

```
internet → Caddy (:443/:80) → Go app (:8080, plain HTTP, inside Docker network)
                ↑
        Let's Encrypt (auto-provisioned by Caddy)
```

Two containers managed by `docker-compose.yml`:

- **`app`** — Go binary listening on `:8080` (plain HTTP), SQLite at `/data/kitchen.db`
- **`caddy`** — Official Caddy image, ports 80/443, reverse-proxies to `app:8080`

SQLite is on a named Docker volume (`kitchen_data`) mounted at `/data` in the `app` container.

Caddy's Let's Encrypt certs and state are on a separate named volume (`caddy_data`).

---

## File Structure (new files)

```
Dockerfile
docker-compose.yml
Caddyfile
.env.example        # committed, documents required vars
.env                # gitignored, actual secrets
```

---

## Environment Variables

| Variable | When used | Description |
|----------|-----------|-------------|
| `SELF_SIGNED_TLS` | Local dev | Set to `true` → app serves HTTPS itself with self-signed cert on `:8443`. When unset, app serves plain HTTP on `:8080`. |
| `OAUTH_ENABLED` | Production | Set to `true` → enable Google OAuth middleware. When unset, all requests pass through unauthenticated. |
| `OAUTH_ALLOWED_EMAILS` | Production | Comma-separated whitelist: `you@gmail.com,partner@gmail.com` |
| `GOOGLE_CLIENT_ID` | Production | From Google Cloud Console |
| `GOOGLE_CLIENT_SECRET` | Production | From Google Cloud Console |
| `SESSION_SECRET` | Production | 32+ byte random string for signing session cookies |
| `BASE_URL` | Production | Full public URL e.g. `https://myinisjap.tplinkdns.com` — used to construct OAuth redirect URI |
| `DB_PATH` | Both | Path to SQLite file. Defaults to `./kitchen.db` (local dev). Set to `/data/kitchen.db` in Docker via `.env`. |

**Local dev workflow (unchanged):**
```bash
SELF_SIGNED_TLS=true go run .
# browse to https://localhost:8443 — no auth, self-signed cert
```

**Production workflow:**
```bash
# fill in .env, then:
docker compose up -d
```

---

## main.go Changes

`main.go` reads env vars at startup to determine mode:

```
if SELF_SIGNED_TLS=true:
    ensureCert() as today
    listenAndServeTLS on :8443
    + HTTP→HTTPS redirect on :8080
else:
    plain HTTP on :8080 (Caddy handles TLS)

if OAUTH_ENABLED=true:
    wrap mux with session manager + auth middleware
```

The TLS and redirect logic currently hardcoded in `main.go` becomes conditional. `tls.go` and `ensureCert()` remain unchanged.

---

## OAuth Implementation

**Dependencies to add:**
- `golang.org/x/oauth2` — OAuth2 client + Google provider
- `github.com/alexedwards/scs/v2` — session management (cookie store, no external dependency)

**New file: `auth.go`**

Contains:
- `newOAuthConfig(baseURL string) *oauth2.Config` — builds Google OAuth config from env vars
- `authMiddleware(next http.Handler) http.Handler` — checks session, redirects to `/auth/login` if missing
- `handleLogin(w, r)` — generates state token, stores in session, redirects to Google
- `handleCallback(w, r)` — exchanges code, fetches userinfo, checks whitelist, sets session, redirects to `/`
- `handleLogout(w, r)` — destroys session, redirects to `/auth/login`

**Routes added when `OAUTH_ENABLED=true`:**
- `GET /auth/login` → `handleLogin`
- `GET /auth/callback` → `handleCallback`
- `GET /auth/logout` → `handleLogout`

**Session:** `scs.Manager` with cookie store. Cookies are HTTP-only, Secure (when not in dev), SameSite=Lax. TTL: 24 hours.

**Whitelist check:** after fetching Google userinfo email, split `OAUTH_ALLOWED_EMAILS` by comma, trim spaces, check membership. Reject with 403 if not found.

**CSRF:** state param is a random 16-byte hex string stored in the session before redirecting to Google, verified in the callback.

---

## Dockerfile

Multi-stage build:
1. `golang:1.26-alpine` builder — `go build -o kitchen_manager`
2. `alpine:latest` runtime — copies binary + `static/` directory

The binary is the only Go artifact; SQLite is pure-Go (no CGO needed with `modernc.org/sqlite`).

---

## docker-compose.yml

```yaml
services:
  app:
    build: .
    env_file: .env
    volumes:
      - kitchen_data:/data
    restart: unless-stopped

  caddy:
    image: caddy:2-alpine
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    restart: unless-stopped

volumes:
  kitchen_data:
  caddy_data:
  caddy_config:
```

---

## Caddyfile

```
myinisjap.tplinkdns.com {
    reverse_proxy app:8080
}
```

Caddy auto-provisions the Let's Encrypt cert for the hostname on first request. HTTP→HTTPS redirect is handled by Caddy automatically.

---

## .env.example

```
# Set to true to serve self-signed HTTPS directly (local dev only)
SELF_SIGNED_TLS=

# OAuth (leave blank to disable)
OAUTH_ENABLED=true
OAUTH_ALLOWED_EMAILS=you@gmail.com,partner@gmail.com
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
SESSION_SECRET=
BASE_URL=https://myinisjap.tplinkdns.com
```

---

## Google Cloud Console Setup (operator steps)

1. Create a project at console.cloud.google.com
2. Enable the "Google+ API" or "Google Identity" (People API)
3. OAuth consent screen → External, add allowed test users (your emails)
4. Credentials → Create OAuth 2.0 Client ID → Web application
5. Authorized redirect URI: `https://myinisjap.tplinkdns.com/auth/callback`
6. Copy Client ID and Secret into `.env`

---

## What Does NOT Change

- All API handlers (`handlers/`)
- All services (`services/`)
- The `units` package
- The frontend (`static/index.html`) — no login UI needed; browser handles OAuth redirect automatically
- Database schema
- `tls.go` — `ensureCert()` still used when `SELF_SIGNED_TLS=true`
