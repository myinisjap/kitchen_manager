package main

import (
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"kitchen_manager/internal/database"
	"kitchen_manager/internal/testutil"
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
	if !strings.Contains(s, `<script type="module" src="app.js"></script>`) {
		t.Error("index.html must load app.js as an ES module")
	}
	if strings.Contains(s, "<style") {
		t.Error("index.html has a <style> block (blocked by CSP)")
	}
	for _, g := range []string{"static/vendor/alpine-*.min.js", "static/vendor/zxing-*.min.js", "static/app.js", "static/js/api.js", "static/theme-init.js", "static/tailwind.css"} {
		if m, _ := filepath.Glob(g); len(m) == 0 {
			t.Errorf("missing %s", g)
		}
	}
}

func TestHealthz(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	h := withHealthz(testutil.NewDB(t), next)
	for _, m := range []string{"GET", "HEAD"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/healthz", nil))
		if rec.Code != 200 {
			t.Fatalf("%s: got %d", m, rec.Code)
		}
		if m == "GET" && rec.Body.String() != "ok" {
			t.Fatalf("body %q", rec.Body.String())
		}
	}
	for _, p := range []string{"/api/inventory/", "/healthz/x"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 401 {
			t.Fatalf("%s: got %d", p, rec.Code)
		}
	}
}

func TestHealthzDBDown(t *testing.T) {
	db, err := database.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	rec := httptest.NewRecorder()
	withHealthz(db, http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 503 {
		t.Fatalf("got %d", rec.Code)
	}
}

// Any fetch with a second argument (method/body) must go through apiFetch,
// whose own inner fetch is the only allowed exception.
func TestMutatingFetchesUseWrapper(t *testing.T) {
	files := []string{"static/app.js"}
	mods, _ := filepath.Glob("static/js/*.js")
	files = append(files, mods...)
	var found []string
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, fetchesWithOptions(string(b), file)...)
	}
	if len(found) != 1 || !strings.HasPrefix(found[0], "fetch(this.baseUrl + path, opts)") {
		t.Fatalf("only apiFetch may call fetch with options; found: %v", found)
	}
}

func fetchesWithOptions(src, file string) []string {
	var found []string
	for i := 0; i < len(src); {
		j := strings.Index(src[i:], "fetch(")
		if j < 0 {
			break
		}
		start := i + j
		i = start + len("fetch(")
		if start > 0 {
			c := src[start-1]
			if c == '.' || c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
				continue
			}
		}
		depth, hasComma := 1, false
		for k := i; k < len(src) && depth > 0; k++ {
			switch ch := src[k]; ch {
			case '\'', '"', '`':
				for k++; k < len(src) && src[k] != ch; k++ {
					if src[k] == '\\' {
						k++
					}
				}
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
			case ',':
				if depth == 1 {
					hasComma = true
				}
			}
		}
		if hasComma {
			line := strings.Count(src[:start], "\n") + 1
			found = append(found, src[start:start+len("fetch(this.baseUrl + path, opts)")]+" @"+file+":"+strconv.Itoa(line))
		}
	}
	return found
}

func TestPWAManifest(t *testing.T) {
	b, err := os.ReadFile("static/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
		Display   string `json:"display"`
		StartURL  string `json:"start_url"`
		Scope     string `json:"scope"`
		Icons     []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Name == "" || m.ShortName == "" {
		t.Error("name/short_name required")
	}
	if m.Display != "standalone" || m.StartURL != "./" || m.Scope != "./" {
		t.Errorf("bad display/start_url/scope: %q %q %q", m.Display, m.StartURL, m.Scope)
	}
	have := map[string]bool{}
	for _, ic := range m.Icons {
		if strings.HasPrefix(ic.Src, "/") || strings.HasPrefix(ic.Src, "http") {
			t.Errorf("icon src must be relative: %s", ic.Src)
		}
		f, err := os.Open(filepath.Join("static", ic.Src))
		if err != nil {
			t.Errorf("icon missing: %v", err)
			continue
		}
		cfg, err := png.DecodeConfig(f)
		f.Close()
		if err != nil {
			t.Errorf("%s: %v", ic.Src, err)
			continue
		}
		if got := strconv.Itoa(cfg.Width) + "x" + strconv.Itoa(cfg.Height); got != ic.Sizes {
			t.Errorf("%s: size %s, declared %s", ic.Src, got, ic.Sizes)
		}
		have[ic.Sizes] = true
	}
	if !have["192x192"] || !have["512x512"] {
		t.Errorf("need 192 and 512 icons, have %v", have)
	}
}

func TestIndexLinksPWA(t *testing.T) {
	idx, _ := os.ReadFile("static/index.html")
	for _, want := range []string{`rel="manifest" href="manifest.json"`, `name="theme-color"`, `rel="apple-touch-icon"`} {
		if !strings.Contains(string(idx), want) {
			t.Errorf("index.html missing %s", want)
		}
	}
	js, _ := os.ReadFile("static/app.js")
	if !regexp.MustCompile(`serviceWorker\.register\(this\.basePath \+ '/sw\.js'`).Match(js) {
		t.Error("app.js must register the service worker via basePath")
	}
}

func TestServiceWorkerShell(t *testing.T) {
	b, err := os.ReadFile("static/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	m := regexp.MustCompile(`SHELL = \[([^\]]*)\]`).FindStringSubmatch(s)
	if m == nil {
		t.Fatal("SHELL not found")
	}
	entries := regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(m[1], -1)
	if len(entries) == 0 {
		t.Fatal("empty SHELL")
	}
	for _, e := range entries {
		p := e[1]
		if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "http") {
			t.Errorf("SHELL entry must be relative: %s", p)
		}
		if p == "./" {
			continue
		}
		if _, err := os.Stat(filepath.Join("static", p)); err != nil {
			t.Errorf("SHELL entry missing: %s", p)
		}
	}
	inShell := map[string]bool{}
	for _, e := range entries {
		inShell[e[1]] = true
	}
	mods, _ := filepath.Glob("static/js/*.js")
	if len(mods) == 0 {
		t.Error("no ES modules found in static/js")
	}
	for _, f := range mods {
		if !inShell["js/"+filepath.Base(f)] {
			t.Errorf("SHELL must list module js/%s", filepath.Base(f))
		}
	}
	for _, bad := range []string{"'/api/", "/kitchen-manager"} {
		if strings.Contains(s, bad) {
			t.Errorf("sw.js must not contain %q", bad)
		}
	}
}

func TestStaticPWAServed(t *testing.T) {
	h := securityHeaders(http.FileServer(http.Dir("./static")))
	for path, ct := range map[string]string{
		"/sw.js":              "javascript",
		"/manifest.json":      "json",
		"/icons/icon-192.png": "image/png",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), ct) {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: no CSP", path)
		}
	}
}
