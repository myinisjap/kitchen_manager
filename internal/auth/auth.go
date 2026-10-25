package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// NewSessionManager creates and returns a configured *scs.SessionManager backed
// by SQLite. db must be the open application database. SESSION_SECRET must be
// set in the environment.
func NewSessionManager(db *sql.DB) *scs.SessionManager {
	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		log.Fatal("SESSION_SECRET env var is required when OAUTH_ENABLED=true")
	}
	key, err := hex.DecodeString(secret)
	if err != nil || len(key) != 32 {
		log.Fatal("SESSION_SECRET must be a 64-character hex string (32 bytes); generate with: openssl rand -hex 32")
	}
	sm := scs.New()
	sm.Store = newSQLiteStore(db, key)
	sm.Lifetime = 24 * time.Hour
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = true
	return sm
}

// userInfoURL is the OAuth userinfo endpoint; a var so tests can override it.
var userInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"

func baseURL() string { return strings.TrimRight(os.Getenv("BASE_URL"), "/") }

// NewOAuthConfig builds an *oauth2.Config from environment variables.
func NewOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  baseURL() + "/auth/callback",
		Scopes:       []string{"openid", "email"},
		Endpoint:     google.Endpoint,
	}
}

// AllowedEmails returns a set of lower-cased email addresses from the
// OAUTH_ALLOWED_EMAILS environment variable.
func AllowedEmails() map[string]bool {
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

type contextKey string

const emailContextKey contextKey = "email"

// EmailFromContext returns the authenticated user's email stored in ctx,
// or "" if auth is disabled or the value is absent.
func EmailFromContext(ctx context.Context) string {
	v, _ := ctx.Value(emailContextKey).(string)
	return v
}

// AuthMiddleware requires an authenticated session for all non-/auth/ routes,
// and injects the user's email into the request context. Unauthenticated
// /api/* and /ws requests get 401 JSON; others redirect to the absolute
// BASE_URL/auth/login (exempt, so no loop).
func AuthMiddleware(sm *scs.SessionManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Auth routes are always exempt
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		email := sm.GetString(r.Context(), "email")
		if email == "" {
			if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
			http.Redirect(w, r, baseURL()+"/auth/login", http.StatusFound)
			return
		}
		ctx := context.WithValue(r.Context(), emailContextKey, email)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// HandleLogin returns an http.HandlerFunc that initiates the OAuth flow.
func HandleLogin(sm *scs.SessionManager, oauthCfg *oauth2.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		state := hex.EncodeToString(b)
		sm.Put(r.Context(), "oauth_state", state)
		http.Redirect(w, r, oauthCfg.AuthCodeURL(state, oauth2.AccessTypeOnline), http.StatusFound)
	}
}

// HandleCallback returns an http.HandlerFunc that handles the OAuth callback.
func HandleCallback(sm *scs.SessionManager, oauthCfg *oauth2.Config, allowed map[string]bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expectedState := sm.GetString(r.Context(), "oauth_state")
		if r.URL.Query().Get("state") != expectedState || expectedState == "" {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		sm.Remove(r.Context(), "oauth_state")

		token, err := oauthCfg.Exchange(r.Context(), r.URL.Query().Get("code"))
		if err != nil {
			http.Error(w, "token exchange failed", http.StatusInternalServerError)
			return
		}

		client := oauthCfg.Client(r.Context(), token)
		resp, err := client.Get(userInfoURL)
		if err != nil {
			http.Error(w, "userinfo fetch failed", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			http.Error(w, "userinfo fetch failed", http.StatusInternalServerError)
			return
		}

		var info struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			http.Error(w, "userinfo decode failed", http.StatusInternalServerError)
			return
		}

		if info.Email == "" {
			http.Error(w, "could not determine email", http.StatusInternalServerError)
			return
		}

		if !allowed[strings.ToLower(info.Email)] {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}

		if err := sm.RenewToken(r.Context()); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		sm.Put(r.Context(), "email", info.Email)
		http.Redirect(w, r, baseURL()+"/", http.StatusFound)
	}
}

// TrustForwardedUser reports whether X-Forwarded-User may be trusted.
func TrustForwardedUser() bool { return os.Getenv("TRUST_FORWARDED_USER") == "true" }

// ForwardedUserMiddleware copies X-Forwarded-User (set by Traefik forward-auth)
// into the request context when no email is there yet. Use it only when
// TRUST_FORWARDED_USER=true, i.e. when the app is reachable only through the proxy.
func ForwardedUserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if EmailFromContext(r.Context()) == "" {
			if u := strings.TrimSpace(r.Header.Get("X-Forwarded-User")); u != "" {
				r = r.WithContext(context.WithValue(r.Context(), emailContextKey, u))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// HandleMe returns the current user's email from the request context (OAuth
// session or trusted forwarded user), falling back to the session.
func HandleMe(sm *scs.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := EmailFromContext(r.Context())
		if email == "" && sm != nil {
			email = sm.GetString(r.Context(), "email")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"email": email})
	}
}

// HandleLogout destroys the session and redirects to the login page.
func HandleLogout(sm *scs.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sm.Destroy(r.Context())
		http.Redirect(w, r, baseURL()+"/auth/login", http.StatusFound)
	}
}
