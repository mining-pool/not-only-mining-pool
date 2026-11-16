package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIOnlyRouting(t *testing.T) {
	s := testServer(t)
	for _, target := range []string{"/", "/api", "/api/", "/probe", "/api/probe"} {
		r := httptest.NewRequest("GET", target, nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s must remain a JSON API: %d %s", target, w.Code, w.Body.String())
		}
	}
	for _, target := range []string{"/admin", "/admin/config", "/index.html", "/assets/app.js", "/api/not-found", "/api/auth/session"} {
		w := call(s, "GET", target, "", nil, "")
		if w.Code != 404 {
			t.Fatalf("unexpected route %s: %d", target, w.Code)
		}
	}
}

func TestCredentialedCORSAndCSRF(t *testing.T) {
	s := testServer(t)
	const frontend = "https://portal.test"
	s.allowedOrigins[frontend] = true // shared immutable configuration in production
	request := func(method, path, body, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://api.test"+path, strings.NewReader(body))
		r.Header.Set("Origin", frontend)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		if method == "OPTIONS" {
			r.Header.Set("Access-Control-Request-Method", "POST")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Header().Get("Access-Control-Allow-Origin") != frontend || w.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatal("missing explicit credentialed CORS headers")
		}
		return w
	}
	w := request("OPTIONS", "/admin-api/auth/login", "", "", nil)
	if w.Code != 204 || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "X-CSRF-Token") {
		t.Fatal("login preflight requires authentication")
	}
	if w := request("GET", "/admin-api/probe", "", "", nil); w.Code != 401 {
		t.Fatal("CORS bypassed authentication")
	}
	w = request("POST", "/admin-api/auth/login", `{"username":"operator","password":"`+testPassword+`"}`, "", nil)
	if w.Code != 200 {
		t.Fatalf("allowed frontend cannot log in: %s", w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if w := request("GET", "/admin-api/probe", "", "", cookie); w.Code != 200 {
		t.Fatal("cross-origin session failed")
	}
	if w := request("POST", "/admin-api/auth/logout", "", "incorrect-token", cookie); w.Code != 403 {
		t.Fatal("CORS replaced CSRF protection")
	}
	s.admin.mu.Lock()
	csrf := s.admin.sessions[tokenKey(cookie.Value)].CSRFToken
	s.admin.mu.Unlock()
	if w := request("POST", "/admin-api/auth/logout", "", csrf, cookie); w.Code != 200 {
		t.Fatal("cross-origin logout failed")
	}
	r := httptest.NewRequest("OPTIONS", "https://api.test/admin-api/auth/login", nil)
	r.Header.Set("Origin", "https://evil.test")
	r.Header.Set("Access-Control-Request-Method", "POST")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("untrusted origin permitted")
	}
}

func TestOriginAndCookieConfiguration(t *testing.T) {
	for _, origin := range []string{"*", "https://*.test", "null", "https://user:pass@api.test", "https://api.test/path", "https://api.test?x=1", "https://api.test#fragment"} {
		if _, err := parseOrigins([]string{origin}); err == nil {
			t.Fatalf("accepted %q", origin)
		}
	}
	origins, err := parseOrigins([]string{"https://Portal.test/", "http://localhost:5173"})
	if err != nil || !origins["https://portal.test"] || !origins["http://localhost:5173"] {
		t.Fatal("valid origins rejected")
	}
	s := testServer(t)
	options := *s.apiConf.Admin
	options.CookieSameSite = "none"
	auth, err := newAdminAuth(&options, origins)
	if err != nil {
		t.Fatal(err)
	}
	s.admin = auth
	cookie, _ := login(t, s, nil)
	if cookie.SameSite != http.SameSiteNoneMode || !cookie.Secure {
		t.Fatal("cross-site cookie is not secure")
	}
	options.AllowInsecureHTTP = true
	if _, err := newAdminAuth(&options, origins); err == nil {
		t.Fatal("allowed insecure SameSite=None")
	}
}
