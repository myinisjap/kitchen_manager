package handlers

import (
	"database/sql"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var allowedSettingKeys = map[string]bool{"default_servings": true, "expiry_window_days": true}

const maxSettingValueLen = 256

func RegisterSettings(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /api/backup", func(w http.ResponseWriter, r *http.Request) {
		dir, err := os.MkdirTemp("", "kitchen-backup-*")
		if err != nil {
			log.Printf("backup: mkdtemp: %v", err)
			WriteError(w, http.StatusInternalServerError, "could not create backup")
			return
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "kitchen.db")
		if _, err := db.ExecContext(r.Context(), "VACUUM INTO ?", path); err != nil {
			log.Printf("backup: vacuum into: %v", err)
			WriteError(w, http.StatusInternalServerError, "could not create backup")
			return
		}
		f, err := os.Open(path)
		if err != nil {
			log.Printf("backup: open: %v", err)
			WriteError(w, http.StatusInternalServerError, "could not create backup")
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			log.Printf("backup: stat: %v", err)
			WriteError(w, http.StatusInternalServerError, "could not create backup")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="kitchen.db"`)
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
		if _, err := io.Copy(w, f); err != nil {
			log.Printf("backup: copy: %v", err)
		}
	})

	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(`SELECT key, value FROM settings`)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		m := map[string]string{}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			m[k] = v
		}
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, m)
	})

	mux.HandleFunc("PATCH /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var patch map[string]string
		if err := ReadJSON(r, &patch); err != nil || len(patch) == 0 {
			WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
		for k, v := range patch {
			if !allowedSettingKeys[k] {
				WriteError(w, http.StatusBadRequest, "unknown setting: "+k)
				return
			}
			if len(v) > maxSettingValueLen {
				WriteError(w, http.StatusBadRequest, "setting value too long")
				return
			}
		}
		if v, ok := patch["default_servings"]; ok {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				WriteError(w, http.StatusBadRequest, "default_servings must be a positive integer")
				return
			}
		}
		if v, ok := patch["expiry_window_days"]; ok {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 365 {
				WriteError(w, http.StatusBadRequest, "expiry_window_days must be an integer between 1 and 365")
				return
			}
		}
		for k, v := range patch {
			if _, err := db.Exec(
				`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
				k, v,
			); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		// Return full updated settings
		rows, err := db.Query(`SELECT key, value FROM settings`)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		m := map[string]string{}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			m[k] = v
		}
		if err := rows.Err(); err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, m)
	})
}

const defaultExpiryWindowDays = 7

// expiryWindowDays reads settings.expiry_window_days, falling back to 7.
func expiryWindowDays(db *sql.DB) int {
	var v string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'expiry_window_days'`).Scan(&v); err != nil {
		return defaultExpiryWindowDays
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 365 {
		return defaultExpiryWindowDays
	}
	return n
}

// expiryDaysAndCutoff uses ?days= when it is an integer, else the setting.
func expiryDaysAndCutoff(r *http.Request, db *sql.DB) (int, string) {
	days := expiryWindowDays(db)
	if s := r.URL.Query().Get("days"); s != "" {
		if d, err := strconv.Atoi(s); err == nil {
			days = d
		}
	}
	return days, time.Now().AddDate(0, 0, days).Format("2006-01-02")
}
