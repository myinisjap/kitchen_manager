package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"kitchen_manager/handlers"
)

func TestAPIGuard(t *testing.T) {
	h := handlers.APIGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	cases := []struct {
		name, method, path, ct, origin string
		want                           int
	}{
		{"no ct", "POST", "/api/x", "", "", 415},
		{"text", "POST", "/api/x", "text/plain", "", 415},
		{"form", "POST", "/api/x", "application/x-www-form-urlencoded", "", 415},
		{"json charset", "POST", "/api/x", "application/json; charset=utf-8", "", 200},
		{"delete json", "DELETE", "/api/x", "application/json", "", 200},
		{"multipart receipt", "POST", "/api/receipt/parse", "multipart/form-data; boundary=x", "", 200},
		{"multipart other", "POST", "/api/inventory/", "multipart/form-data; boundary=x", "", 415},
		{"get", "GET", "/api/x", "", "", 200},
		{"logout", "POST", "/auth/logout", "", "", 200},
		{"evil origin", "POST", "/api/x", "application/json", "https://evil.example", 403},
		{"same origin", "POST", "/api/x", "application/json", "https://example.com", 200},
		{"null origin", "POST", "/api/x", "application/json", "null", 403},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		req.Host = "example.com"
		if c.ct != "" {
			req.Header.Set("Content-Type", c.ct)
		}
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("%s: got %d want %d", c.name, w.Code, c.want)
		}
	}
}

func TestWSOrigin(t *testing.T) {
	hub := handlers.NewHub()
	go hub.Run()
	srv := httptest.NewServer(http.HandlerFunc(handlers.ServeWs(hub)))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	dial := func(origin string) (*http.Response, error) {
		hdr := http.Header{}
		if origin != "" {
			hdr.Set("Origin", origin)
		}
		c, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
		if c != nil {
			c.Close()
		}
		return resp, err
	}
	if resp, err := dial("http://evil.example"); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Errorf("evil origin not rejected: %v", err)
	}
	if _, err := dial(srv.URL); err != nil {
		t.Errorf("same origin: %v", err)
	}
	if _, err := dial(""); err != nil {
		t.Errorf("no origin: %v", err)
	}
	t.Setenv("BASE_URL", "https://kitchen.example/km")
	if _, err := dial("https://kitchen.example"); err != nil {
		t.Errorf("base url origin: %v", err)
	}
}
