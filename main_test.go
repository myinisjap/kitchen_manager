package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusNotFound} {
		h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		csp := rec.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "script-src 'self' 'unsafe-eval'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("status %d: CSP missing %q: %s", code, want, csp)
			}
		}
		for _, bad := range []string{"'unsafe-inline'", "cdn.", "unpkg"} {
			if strings.Contains(csp, bad) {
				t.Errorf("CSP must not contain %q", bad)
			}
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q", got)
		}
		if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
			t.Errorf("Referrer-Policy = %q", got)
		}
		if rec.Code != code {
			t.Errorf("status = %d, want %d", rec.Code, code)
		}
	}
}

func TestIndexHasNoExternalOrInlineCode(t *testing.T) {
	b, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if regexp.MustCompile(`(?i)<(script|link)[^>]*(src|href)="https?://`).MatchString(s) {
		t.Error("index.html loads an external script/stylesheet")
	}
	if regexp.MustCompile(`(?is)<script[^>]*>\s*[^<\s][^<]*</script>`).MatchString(s) {
		t.Error("index.html has an inline script (blocked by CSP)")
	}
	if strings.Contains(s, "<style") {
		t.Error("index.html has a <style> block (blocked by CSP)")
	}
	for _, g := range []string{"static/vendor/alpine-*.min.js", "static/vendor/zxing-*.min.js", "static/app.js", "static/theme-init.js", "static/tailwind.css"} {
		if m, _ := filepath.Glob(g); len(m) == 0 {
			t.Errorf("missing %s", g)
		}
	}
}
