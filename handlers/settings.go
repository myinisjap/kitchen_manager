package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
)

func RegisterSettings(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /api/backup", func(w http.ResponseWriter, r *http.Request) {
		dbPath := os.Getenv("DB_PATH")
		if dbPath == "" {
			dbPath = "./kitchen.db"
		}
		f, err := os.Open(dbPath)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not open database")
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not stat database")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="kitchen.db"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
		io.Copy(w, f)
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
		if v, ok := patch["default_servings"]; ok {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				WriteError(w, http.StatusBadRequest, "default_servings must be a positive integer")
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
