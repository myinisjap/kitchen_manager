package handlers

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

// SameOrigin reports whether the request's Origin header (if any) matches the
// request Host or the BASE_URL host. Requests without Origin are allowed.
func SameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if base := os.Getenv("BASE_URL"); base != "" {
		if bu, err := url.Parse(base); err == nil && bu.Host != "" && strings.EqualFold(u.Host, bu.Host) {
			return true
		}
	}
	return false
}
