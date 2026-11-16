package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func parseOrigins(values []string) (map[string]bool, error) {
	origins := make(map[string]bool, len(values))
	for _, value := range values {
		origin, err := url.Parse(value)
		if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" ||
			strings.Contains(origin.Host, "*") || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" ||
			(origin.Path != "" && origin.Path != "/") {
			return nil, fmt.Errorf("invalid api.allowedOrigins entry %q: use an exact HTTP(S) origin", value)
		}
		origins[origin.Scheme+"://"+strings.ToLower(origin.Host)] = true
	}
	return origins, nil
}

func allowedOrigin(r *http.Request, allowed map[string]bool) bool {
	if sameOrigin(r) {
		return true
	}
	origin, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == "" &&
		allowed[origin.Scheme+"://"+strings.ToLower(origin.Host)]
}

// ServeHTTP exposes JSON APIs only. The React SPA is built and served separately.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Origin")
	if origin := r.Header.Get("Origin"); origin != "" {
		if !allowedOrigin(r, s.origins.get()) {
			apiError(w, http.StatusForbidden, "origin_mismatch", "Request origin is not allowed.")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		if r.Header.Get("Origin") == "" || !allowedOrigin(r, s.origins.get()) {
			apiError(w, http.StatusForbidden, "origin_mismatch", "Request origin is not allowed.")
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/admin-api" || strings.HasPrefix(r.URL.Path, "/admin-api/") {
		http.StripPrefix("/admin-api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			s.admin().handle(w, r, s.adminRouter)
		})).ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		http.StripPrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			s.Router.ServeHTTP(w, r)
		})).ServeHTTP(w, r)
		return
	}
	// Keep the original unprefixed mining-pool API for existing clients.
	s.Router.ServeHTTP(w, r)
}
