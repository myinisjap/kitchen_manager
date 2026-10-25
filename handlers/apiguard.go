package handlers

import (
	"mime"
	"net/http"
	"strings"
)

var multipartPaths = map[string]bool{
	"/api/receipt/parse":      true,
	"/api/recipes/import/pdf": true,
}

// APIGuard rejects cross-origin mutating /api/ requests and requires a JSON
// Content-Type (multipart only for upload endpoints).
func APIGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if strings.HasPrefix(r.URL.Path, "/api/") {
				if !SameOrigin(r) {
					WriteError(w, http.StatusForbidden, "cross-origin request rejected")
					return
				}
				mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if mt != "application/json" && !(mt == "multipart/form-data" && multipartPaths[r.URL.Path]) {
					WriteError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
