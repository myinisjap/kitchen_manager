package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"golang.org/x/oauth2"
)

const testBase = "https://example.test/kitchen-manager"

func setup(t *testing.T, cfg *oauth2.Config) (*scs.SessionManager, http.Handler) {
	t.Helper()
	t.Setenv("BASE_URL", testBase)
	sm := scs.New()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", HandleLogin(sm, cfg))
	mux.HandleFunc("/auth/callback", HandleCallback(sm, cfg, map[string]bool{"allowed@example.com": true}))
	ok := func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(EmailFromContext(r.Context()))) }
	mux.HandleFunc("/api/items", ok)
	mux.HandleFunc("/ws", ok)
	mux.HandleFunc("/", ok)
	return sm, sm.LoadAndSave(AuthMiddleware(sm, mux))
}

func do(h http.Handler, path, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c.Value
		}
	}
	return ""
}

func testCfg(srv string) *oauth2.Config {
	return &oauth2.Config{ClientID: "id", RedirectURL: "http://x/cb",
		Endpoint: oauth2.Endpoint{AuthURL: srv + "/auth", TokenURL: srv + "/token"}}
}

func TestUnauthenticatedRootRedirectsToLogin(t *testing.T) {
	_, h := setup(t, testCfg("https://idp.test"))
	for _, p := range []string{"/", "/app.js"} {
		rec := do(h, p, "")
		if rec.Code != 302 || rec.Header().Get("Location") != testBase+"/auth/login" {
			t.Fatalf("%s: got %d %q", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	rec := do(h, "/auth/login", "")
	if rec.Code != 302 || !strings.HasPrefix(rec.Header().Get("Location"), "https://idp.test/auth") {
		t.Fatalf("login looped: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestUnauthenticatedAPIReturns401(t *testing.T) {
	_, h := setup(t, testCfg("https://idp.test"))
	for _, p := range []string{"/api/items", "/ws"} {
		rec := do(h, p, "")
		if rec.Code != 401 || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") ||
			rec.Header().Get("Location") != "" || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Fatalf("%s: %d %v %s", p, rec.Code, rec.Header(), rec.Body.String())
		}
	}
}

func TestCallbackRenewsToken(t *testing.T) {
	idp := http.NewServeMux()
	idp.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"x","token_type":"Bearer"}`))
	})
	idp.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"email":"Allowed@Example.com"}`))
	})
	srv := httptest.NewServer(idp)
	defer srv.Close()
	old := userInfoURL
	userInfoURL = srv.URL + "/userinfo"
	t.Cleanup(func() { userInfoURL = old })

	_, h := setup(t, testCfg(srv.URL))
	rec := do(h, "/auth/login", "")
	t1 := sessionCookie(rec)
	u, _ := url.Parse(rec.Header().Get("Location"))
	state := u.Query().Get("state")
	if t1 == "" || state == "" {
		t.Fatal("missing session or state")
	}

	if r := do(h, "/auth/callback?state=bad&code=abc", t1); r.Code != 400 {
		t.Fatalf("bad state: %d", r.Code)
	}

	rec = do(h, "/auth/callback?state="+state+"&code=abc", t1)
	if rec.Code != 302 || rec.Header().Get("Location") != testBase+"/" {
		t.Fatalf("callback: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	t2 := sessionCookie(rec)
	if t2 == "" || t2 == t1 {
		t.Fatalf("token not renewed: %q vs %q", t1, t2)
	}
	if r := do(h, "/api/items", t2); r.Code != 200 || r.Body.String() != "Allowed@Example.com" {
		t.Fatalf("new token: %d %s", r.Code, r.Body.String())
	}
	if r := do(h, "/api/items", t1); r.Code != 401 {
		t.Fatalf("old token: %d", r.Code)
	}
}
