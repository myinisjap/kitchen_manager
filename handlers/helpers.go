package handlers

import (
	"encoding/json"
	"log"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func ReadJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, MaxJSONBodyBytes)
	return json.NewDecoder(r.Body).Decode(v)
}

func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

const msgInternalError = "internal server error"

// WriteServerError logs the real error and writes a generic 500.
func WriteServerError(w http.ResponseWriter, r *http.Request, err error) {
	if r != nil {
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	} else {
		log.Printf("server error: %v", err)
	}
	WriteError(w, http.StatusInternalServerError, msgInternalError)
}
