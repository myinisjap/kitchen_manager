package handlers_test

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"kitchen_manager/handlers"
	"kitchen_manager/internal/testutil"

	_ "modernc.org/sqlite"
)

func TestBackupIsConsistentSQLiteWithCurrentRows(t *testing.T) {
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	handlers.RegisterSettings(mux, db)

	if _, err := db.Exec(`INSERT INTO inventory (name, quantity, unit) VALUES ('Flour', 2, 'kg')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatal(err)
	}

	w := call(t, mux, "GET", "/api/backup", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "kitchen.db") {
		t.Errorf("bad disposition")
	}
	body := w.Body.Bytes()
	if !strings.HasPrefix(string(body), "SQLite format 3\x00") {
		t.Fatal("not a sqlite file")
	}
	if w.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Errorf("content-length mismatch")
	}

	p := filepath.Join(t.TempDir(), "backup.db")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	bdb, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer bdb.Close()
	var res string
	if err := bdb.QueryRow(`PRAGMA integrity_check`).Scan(&res); err != nil || res != "ok" {
		t.Fatalf("integrity: %v %q", err, res)
	}
	var name string
	var qty float64
	if err := bdb.QueryRow(`SELECT name, quantity FROM inventory WHERE name='Flour'`).Scan(&name, &qty); err != nil || qty != 2 {
		t.Fatalf("inventory row: %v %v", err, qty)
	}
	var v string
	if err := bdb.QueryRow(`SELECT value FROM settings WHERE key='k'`).Scan(&v); err != nil || v != "v" {
		t.Fatalf("settings row: %v %q", err, v)
	}
}

func TestSettingsPatchAllowlist(t *testing.T) {
	db := testutil.NewDB(t)
	mux := http.NewServeMux()
	handlers.RegisterSettings(mux, db)

	if w := call(t, mux, "PATCH", "/api/settings", `{"default_servings":"4"}`); w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if w := call(t, mux, "GET", "/api/settings", ""); !strings.Contains(w.Body.String(), `"default_servings":"4"`) {
		t.Fatalf("got %s", w.Body.String())
	}
	for _, body := range []string{
		`{"evil":"x"}`,
		`{"default_servings":"5","evil":"x"}`,
		`{"default_servings":"` + strings.Repeat("1", 300) + `"}`,
	} {
		if w := call(t, mux, "PATCH", "/api/settings", body); w.Code != 400 {
			t.Errorf("%.40s: status %d", body, w.Code)
		}
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key='evil'`).Scan(&n)
	if n != 0 {
		t.Error("evil key stored")
	}
	var v string
	db.QueryRow(`SELECT value FROM settings WHERE key='default_servings'`).Scan(&v)
	if v != "4" {
		t.Errorf("value changed to %q", v)
	}
}
