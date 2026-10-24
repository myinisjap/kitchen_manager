package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "127.1.2.3", "10.0.0.1", "172.16.5.4", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "0.0.0.0", "100.64.0.1", "100.127.255.255", "224.0.0.1", "255.255.255.255",
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254", "64:ff9b::7f00:1"}
	allowed := []string{"8.8.8.8", "1.1.1.1", "172.32.0.1", "100.128.0.1", "93.184.216.34", "2606:4700:4700::1111", "::ffff:8.8.8.8"}
	for _, s := range blocked {
		if !isBlockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range allowed {
		if isBlockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestSafeDialControl(t *testing.T) {
	for _, a := range []string{"127.0.0.1:80", "[::1]:443"} {
		if err := safeDialControl("tcp", a, nil); !errors.Is(err, errBlockedAddress) {
			t.Errorf("%s: got %v", a, err)
		}
	}
	if err := safeDialControl("tcp4", "8.8.8.8:443", nil); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestImportURLRefusesLoopback(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		w.Write([]byte("<html>secret</html>"))
	}))
	defer srv.Close()
	mux := http.NewServeMux()
	RegisterRecipeImport(mux)
	for _, u := range []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)} {
		req := httptest.NewRequest(http.MethodPost, "/api/recipes/import/url", strings.NewReader(`{"url":"`+u+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "private") {
			t.Errorf("%s: got %d %s", u, rec.Code, rec.Body.String())
		}
	}
	if hit.Load() {
		t.Error("loopback server was contacted")
	}
}

func TestSafeClientRefusesLoopbackDirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, err := newSafeHTTPClient(5 * time.Second).Get(srv.URL)
	if !errors.Is(err, errBlockedAddress) {
		t.Fatalf("got %v", err)
	}
}

func TestCheckRedirect(t *testing.T) {
	c := newSafeHTTPClient(time.Second)
	mk := func(u string) *http.Request { r, _ := http.NewRequest("GET", u, nil); return r }
	if c.CheckRedirect(mk("https://example.com"), make([]*http.Request, 5)) == nil {
		t.Error("expected too-many-redirects error")
	}
	for _, u := range []string{"file:///etc/passwd", "ftp://x/", "gopher://x/"} {
		if c.CheckRedirect(mk(u), nil) == nil {
			t.Errorf("%s should be rejected", u)
		}
	}
	if err := c.CheckRedirect(mk("https://example.com"), make([]*http.Request, 2)); err != nil {
		t.Error(err)
	}
}

func TestValidateFetchURL(t *testing.T) {
	ctx := context.Background()
	for _, u := range []string{"ftp://example.com", "file:///etc/passwd", "javascript:alert(1)", "http://", "http://[::ffff:127.0.0.1]/", "http://169.254.169.254/", "http://user:pw@8.8.8.8/"} {
		if _, err := validateFetchURL(ctx, u); err == nil {
			t.Errorf("%s should be rejected", u)
		}
	}
	if _, err := validateFetchURL(ctx, "http://8.8.8.8/"); err != nil {
		t.Error(err)
	}
}
