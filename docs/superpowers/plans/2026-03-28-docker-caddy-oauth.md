# Docker + Caddy + Google OAuth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Containerize kitchen-manager behind Caddy (Let's Encrypt TLS), add Google OAuth with email whitelist, and keep local dev working with self-signed TLS and no auth via env vars.

**Architecture:** Two Docker containers — `app` (Go binary, plain HTTP :8080, SQLite on a named volume) and `caddy` (reverse proxy, TLS termination). `main.go` reads env vars at startup to decide whether to serve self-signed TLS or plain HTTP, and whether to enable OAuth middleware. A new `auth.go` file handles the full OAuth flow and session management.

**Tech Stack:** `golang.org/x/oauth2`, `github.com/alexedwards/scs/v2`, Caddy 2, Docker Compose, Google OAuth 2.0 / userinfo API

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `main.go` | Modify | Read `DB_PATH`, `SELF_SIGNED_TLS`, `OAUTH_ENABLED` env vars; conditional TLS and auth middleware |
| `auth.go` | Create | OAuth config, session manager, login/callback/logout handlers, auth middleware |
| `Dockerfile` | Create | Multi-stage build: alpine builder → alpine runtime |
| `docker-compose.yml` | Create | `app` + `caddy` services, named volumes |
| `Caddyfile` | Create | Reverse proxy config for the DDNS hostname |
| `.env.example` | Create | Documents all env vars |
| `.gitignore` | Modify | Add `.env` and `kitchen.db` if not already ignored |
| `go.mod` / `go.sum` | Modify | Add `golang.org/x/oauth2` and `alexedwards/scs/v2` |

---

## Task 1: Add dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add oauth2 and scs dependencies**

```bash
cd /path/to/kitchen_manager
go get golang.org/x/oauth2@latest
go get github.com/alexedwards/scs/v2@latest
```

- [ ] **Step 2: Verify modules downloaded**

```bash
grep -E "oauth2|scs" go.mod
```

Expected output includes both:
```
github.com/alexedwards/scs/v2 v2.x.x
golang.org/x/oauth2 v0.x.x
```

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add oauth2 and scs session management"
```

---

## Task 2: Make DB path configurable

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Update main.go to read DB_PATH env var**

Replace the `openDB("./kitchen.db")` call in `main.go` with:

```go
package main

import (
	"log"
	"net"
	"net/http"
	"os"

	"kitchen_manager/handlers"
)

const (
	httpAddr  = ":8080"
	httpsAddr = ":8443"
	certFile  = "./cert.pem"
	keyFile   = "./key.pem"
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./kitchen.db"
	}
	if err := openDB(dbPath); err != nil {
		log.Fatal("db open:", err)
	}
	defer db.Close()

	if err := ensureCert(certFile, keyFile); err != nil {
		log.Fatal("tls cert:", err)
	}

	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterCalendar(mux, db)
	mux.Handle("/", http.FileServer(http.Dir("./static")))

	// HTTP → HTTPS redirect
	go func() {
		log.Println("HTTP redirect listening on", httpAddr)
		log.Fatal(http.ListenAndServe(httpAddr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := parseHost(r.Host)
			if err != nil {
				host = r.Host
			}
			http.Redirect(w, r, "https://"+host+httpsAddr+r.RequestURI, http.StatusMovedPermanently)
		})))
	}()

	log.Println("HTTPS listening on", httpsAddr)
	log.Fatal(http.ListenAndServeTLS(httpsAddr, certFile, keyFile, mux))
}

func parseHost(hostport string) (string, string, error) {
	host, port, err := net.SplitHostPort(hostport)
	return host, port, err
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./...
```

Expected: no output (success).

- [ ] **Step 3: Commit**

```bash
git add main.go
git commit -m "feat: read DB_PATH from env, default to ./kitchen.db"
```

---

## Task 3: Make TLS conditional in main.go

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Replace main.go with conditional TLS logic**

When `SELF_SIGNED_TLS=true`, behave as today (self-signed cert, HTTPS on :8443). Otherwise serve plain HTTP on :8080 for Caddy to front.

```go
package main

import (
	"log"
	"net"
	"net/http"
	"os"

	"kitchen_manager/handlers"
)

const (
	httpAddr  = ":8080"
	httpsAddr = ":8443"
	certFile  = "./cert.pem"
	keyFile   = "./key.pem"
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./kitchen.db"
	}
	if err := openDB(dbPath); err != nil {
		log.Fatal("db open:", err)
	}
	defer db.Close()

	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterCalendar(mux, db)
	mux.Handle("/", http.FileServer(http.Dir("./static")))

	if os.Getenv("SELF_SIGNED_TLS") == "true" {
		if err := ensureCert(certFile, keyFile); err != nil {
			log.Fatal("tls cert:", err)
		}
		go func() {
			log.Println("HTTP redirect listening on", httpAddr)
			log.Fatal(http.ListenAndServe(httpAddr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				host, _, err := parseHost(r.Host)
				if err != nil {
					host = r.Host
				}
				http.Redirect(w, r, "https://"+host+httpsAddr+r.RequestURI, http.StatusMovedPermanently)
			})))
		}()
		log.Println("HTTPS listening on", httpsAddr)
		log.Fatal(http.ListenAndServeTLS(httpsAddr, certFile, keyFile, mux))
	} else {
		log.Println("HTTP listening on", httpAddr)
		log.Fatal(http.ListenAndServe(httpAddr, mux))
	}
}

func parseHost(hostport string) (string, string, error) {
	host, port, err := net.SplitHostPort(hostport)
	return host, port, err
}
```

- [ ] **Step 2: Test self-signed mode still works**

```bash
SELF_SIGNED_TLS=true go run . &
sleep 1
curl -k https://localhost:8443/api/inventory/
kill %1
```

Expected: JSON array response (empty or with items).

- [ ] **Step 3: Test plain HTTP mode**

```bash
go run . &
sleep 1
curl http://localhost:8080/api/inventory/
kill %1
```

Expected: JSON array response.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: conditional TLS — SELF_SIGNED_TLS=true for local dev, plain HTTP otherwise"
```

---

## Task 4: Create auth.go with OAuth flow

**Files:**
- Create: `auth.go`

- [ ] **Step 1: Create auth.go**

```go
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var sessionManager *scs.SessionManager

func newSessionManager() *scs.SessionManager {
	sm := scs.New()
	sm.Lifetime = 24 * time.Hour
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = os.Getenv("SELF_SIGNED_TLS") != "true"
	return sm
}

func newOAuthConfig() *oauth2.Config {
	baseURL := os.Getenv("BASE_URL")
	return &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  baseURL + "/auth/callback",
		Scopes:       []string{"openid", "email"},
		Endpoint:     google.Endpoint,
	}
}

func allowedEmails() map[string]bool {
	raw := os.Getenv("OAUTH_ALLOWED_EMAILS")
	m := make(map[string]bool)
	for _, e := range strings.Split(raw, ",") {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			m[e] = true
		}
	}
	return m
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Auth routes are always exempt
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		email := sessionManager.GetString(r.Context(), "email")
		if email == "" {
			http.Redirect(w, r, "/auth/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleLogin(oauthCfg *oauth2.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		state := hex.EncodeToString(b)
		sessionManager.Put(r.Context(), "oauth_state", state)
		http.Redirect(w, r, oauthCfg.AuthCodeURL(state, oauth2.AccessTypeOnline), http.StatusFound)
	}
}

func handleCallback(oauthCfg *oauth2.Config, allowed map[string]bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expectedState := sessionManager.GetString(r.Context(), "oauth_state")
		if r.URL.Query().Get("state") != expectedState || expectedState == "" {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		sessionManager.Remove(r.Context(), "oauth_state")

		token, err := oauthCfg.Exchange(context.Background(), r.URL.Query().Get("code"))
		if err != nil {
			http.Error(w, "token exchange failed", http.StatusInternalServerError)
			return
		}

		client := oauthCfg.Client(context.Background(), token)
		resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
		if err != nil {
			http.Error(w, "userinfo fetch failed", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		var info struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			http.Error(w, "userinfo decode failed", http.StatusInternalServerError)
			return
		}

		if !allowed[strings.ToLower(info.Email)] {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}

		sessionManager.Put(r.Context(), "email", info.Email)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	sessionManager.Destroy(r.Context())
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./...
```

Expected: no output (success).

- [ ] **Step 3: Commit**

```bash
git add auth.go
git commit -m "feat: Google OAuth flow with email whitelist and scs session management"
```

---

## Task 5: Wire OAuth into main.go

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Update main.go to wire in session manager and auth middleware when OAUTH_ENABLED=true**

```go
package main

import (
	"log"
	"net"
	"net/http"
	"os"

	"kitchen_manager/handlers"
)

const (
	httpAddr  = ":8080"
	httpsAddr = ":8443"
	certFile  = "./cert.pem"
	keyFile   = "./key.pem"
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./kitchen.db"
	}
	if err := openDB(dbPath); err != nil {
		log.Fatal("db open:", err)
	}
	defer db.Close()

	mux := http.NewServeMux()
	handlers.RegisterInventory(mux, db)
	handlers.RegisterShopping(mux, db)
	handlers.RegisterRecipes(mux, db)
	handlers.RegisterCalendar(mux, db)
	mux.Handle("/", http.FileServer(http.Dir("./static")))

	var handler http.Handler = mux

	if os.Getenv("OAUTH_ENABLED") == "true" {
		sessionManager = newSessionManager()
		oauthCfg := newOAuthConfig()
		allowed := allowedEmails()

		mux.HandleFunc("/auth/login", handleLogin(oauthCfg))
		mux.HandleFunc("/auth/callback", handleCallback(oauthCfg, allowed))
		mux.HandleFunc("/auth/logout", handleLogout)

		handler = sessionManager.LoadAndSave(authMiddleware(mux))
	}

	if os.Getenv("SELF_SIGNED_TLS") == "true" {
		if err := ensureCert(certFile, keyFile); err != nil {
			log.Fatal("tls cert:", err)
		}
		go func() {
			log.Println("HTTP redirect listening on", httpAddr)
			log.Fatal(http.ListenAndServe(httpAddr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				host, _, err := parseHost(r.Host)
				if err != nil {
					host = r.Host
				}
				http.Redirect(w, r, "https://"+host+httpsAddr+r.RequestURI, http.StatusMovedPermanently)
			})))
		}()
		log.Println("HTTPS listening on", httpsAddr)
		log.Fatal(http.ListenAndServeTLS(httpsAddr, certFile, keyFile, handler))
	} else {
		log.Println("HTTP listening on", httpAddr)
		log.Fatal(http.ListenAndServe(httpAddr, handler))
	}
}

func parseHost(hostport string) (string, string, error) {
	host, port, err := net.SplitHostPort(hostport)
	return host, port, err
}
```

- [ ] **Step 2: Test plain mode still works (no env vars)**

```bash
go run . &
sleep 1
curl -s http://localhost:8080/api/inventory/ | head -c 50
kill %1
```

Expected: JSON response, no redirect.

- [ ] **Step 3: Test self-signed TLS mode still works**

```bash
SELF_SIGNED_TLS=true go run . &
sleep 1
curl -sk https://localhost:8443/api/inventory/ | head -c 50
kill %1
```

Expected: JSON response.

- [ ] **Step 4: Test OAuth redirect fires when enabled**

```bash
OAUTH_ENABLED=true GOOGLE_CLIENT_ID=test GOOGLE_CLIENT_SECRET=test SESSION_SECRET=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa BASE_URL=http://localhost:8080 go run . &
sleep 1
curl -s -o /dev/null -w "%{http_code} %{redirect_url}" http://localhost:8080/
kill %1
```

Expected: `302 http://localhost:8080/auth/login`

- [ ] **Step 5: Commit**

```bash
git add main.go
git commit -m "feat: wire OAuth middleware and session manager into main.go"
```

---

## Task 6: Create Dockerfile

**Files:**
- Create: `Dockerfile`

- [ ] **Step 1: Create Dockerfile**

```dockerfile
# syntax=docker/dockerfile:1

# ---- builder ----
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o kitchen_manager .

# ---- runtime ----
FROM alpine:latest
WORKDIR /app
COPY --from=builder /src/kitchen_manager .
COPY --from=builder /src/static ./static
EXPOSE 8080
CMD ["./kitchen_manager"]
```

- [ ] **Step 2: Build the image to verify it compiles**

```bash
docker build -t kitchen-manager:test .
```

Expected: `Successfully built ...` with no errors.

- [ ] **Step 3: Smoke test the image**

```bash
docker run --rm -p 8080:8080 kitchen-manager:test &
sleep 2
curl -s http://localhost:8080/api/inventory/ | head -c 50
docker stop $(docker ps -q --filter ancestor=kitchen-manager:test)
```

Expected: JSON response.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile
git commit -m "feat: multi-stage Dockerfile for kitchen-manager"
```

---

## Task 7: Create docker-compose.yml and Caddyfile

**Files:**
- Create: `docker-compose.yml`
- Create: `Caddyfile`

- [ ] **Step 1: Create Caddyfile**

```
myinisjap.tplinkdns.com {
    reverse_proxy app:8080
}
```

- [ ] **Step 2: Create docker-compose.yml**

```yaml
services:
  app:
    build: .
    env_file: .env
    volumes:
      - kitchen_data:/data
    restart: unless-stopped
    depends_on: []

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
    depends_on:
      - app

volumes:
  kitchen_data:
  caddy_data:
  caddy_config:
```

- [ ] **Step 3: Validate compose file**

```bash
docker compose config
```

Expected: rendered YAML with no errors.

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml Caddyfile
git commit -m "feat: docker-compose with Caddy reverse proxy and named volumes"
```

---

## Task 8: Create .env.example and update .gitignore

**Files:**
- Create: `.env.example`
- Modify: `.gitignore`

- [ ] **Step 1: Create .env.example**

```
# Path to SQLite database (set to /data/kitchen.db in Docker)
DB_PATH=/data/kitchen.db

# Set to true to serve self-signed HTTPS directly (local dev only, do not use in Docker)
SELF_SIGNED_TLS=

# OAuth — leave OAUTH_ENABLED blank to disable (local dev)
OAUTH_ENABLED=true
OAUTH_ALLOWED_EMAILS=you@gmail.com,partner@gmail.com
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=

# Generate with: openssl rand -hex 32
SESSION_SECRET=

# Full public URL used to build the OAuth redirect URI
BASE_URL=https://myinisjap.tplinkdns.com
```

- [ ] **Step 2: Check current .gitignore**

```bash
cat .gitignore 2>/dev/null || echo "(no .gitignore)"
```

- [ ] **Step 3: Ensure .env and kitchen.db are gitignored**

If `.gitignore` doesn't already include them, append:

```bash
cat >> .gitignore << 'EOF'
.env
kitchen.db
cert.pem
key.pem
EOF
```

- [ ] **Step 4: Verify .env and kitchen.db are ignored**

```bash
git check-ignore -v .env kitchen.db cert.pem key.pem
```

Expected: each file listed with the matching .gitignore rule.

- [ ] **Step 5: Commit**

```bash
git add .env.example .gitignore
git commit -m "chore: add .env.example and ensure secrets are gitignored"
```

---

## Task 9: End-to-end local Docker test

**Files:** none (verification only)

- [ ] **Step 1: Create a minimal .env for local testing**

```bash
cat > .env << 'EOF'
DB_PATH=/data/kitchen.db
EOF
```

- [ ] **Step 2: Start services**

```bash
docker compose up -d --build
```

- [ ] **Step 3: Verify app is reachable through Caddy on localhost**

Since we can't test the real DDNS hostname locally, test that the app responds directly:

```bash
curl -s http://localhost:8080/api/inventory/
```

Expected: JSON array.

- [ ] **Step 4: Verify SQLite volume persists across restarts**

```bash
# Add an item
curl -s -X POST http://localhost:8080/api/inventory/ \
  -H "Content-Type: application/json" \
  -d '{"name":"test-item","quantity":1,"unit":"piece"}'

# Restart the app container
docker compose restart app

# Confirm item still exists
curl -s http://localhost:8080/api/inventory/ | grep test-item
```

Expected: `test-item` appears after restart.

- [ ] **Step 5: Tear down and clean up test data**

```bash
docker compose down
rm .env
```

- [ ] **Step 6: Commit nothing** — this task is verification only. If any issues were fixed, commit those fixes with appropriate messages before this step.

---

## Task 10: Update CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add Docker and auth sections to CLAUDE.md**

Add the following to `CLAUDE.md` after the existing Commands section:

```markdown
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
| `SESSION_SECRET` | — | 32-byte hex string for cookie signing |
| `BASE_URL` | — | Public URL, used for OAuth redirect URI |

## Auth

`auth.go` contains the full OAuth flow. When `OAUTH_ENABLED` is unset, the `authMiddleware` is not added and all requests pass through unauthenticated. Session cookies are managed by `alexedwards/scs` (cookie store, no external dependency).
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: add Docker, running modes, and auth env vars to CLAUDE.md"
```
