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
