package api

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mining-pool/not-only-mining-pool/config"
	"golang.org/x/crypto/bcrypt"
)

const testPassword = "test-only-password-123"

func testServer(t *testing.T) *Server {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), 10)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "password.hash")
	if err := os.WriteFile(file, hash, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(&config.Options{API: &config.APIOptions{
		Host: "127.0.0.1", Port: 20000,
		Admin: &config.AdminOptions{Username: "operator", PasswordHashFile: file, SessionTTLSeconds: 60},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterFunc("/probe", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, map[string]bool{"ok": true}) })
	return s
}

func call(s *Server, method, target, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://pool.test"+target, strings.NewReader(body))
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Origin", "https://pool.test")
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func login(t *testing.T, s *Server, old *http.Cookie) (*http.Cookie, adminSession) {
	t.Helper()
	w := call(s, "POST", "/admin-api/auth/login", `{"username":"operator","password":"`+testPassword+`"}`, old, "")
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var session adminSession
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies: %v", cookies)
	}
	return cookies[0], session
}

func TestAdminSessionLifecycle(t *testing.T) {
	s := testServer(t)
	for _, target := range []string{"/admin-api", "/admin-api/probe", "/admin-api/auth/session"} {
		if w := call(s, "GET", target, "", nil, ""); w.Code != 401 {
			t.Fatalf("%s: %d", target, w.Code)
		}
	}
	if w := call(s, "GET", "/api/probe", "", nil, ""); w.Code != 200 {
		t.Fatal("public API requires login")
	}
	if w := call(s, "GET", "/probe", "", nil, ""); w.Code != 200 {
		t.Fatal("legacy API broken")
	}
	if w := call(s, "POST", "/admin-api/auth/login", `{"username":"operator","password":"wrong"}`, nil, ""); w.Code != 401 {
		t.Fatal("wrong password accepted")
	}
	cookie, session := login(t, s, nil)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/admin-api" || cookie.MaxAge != 60 {
		t.Fatalf("unsafe session cookie: %+v", cookie)
	}
	if len(cookie.Value) != 43 || len(session.CSRFToken) != 43 || session.Username != "operator" {
		t.Fatal("invalid session")
	}
	for _, target := range []string{"/admin-api/probe", "/admin-api/auth/session", "/admin-api/"} {
		w := call(s, "GET", target, "", cookie, "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d", target, w.Code)
		}
	}
	rotated, session := login(t, s, cookie)
	if rotated.Value == cookie.Value {
		t.Fatal("login did not rotate session")
	}
	if w := call(s, "GET", "/admin-api/probe", "", cookie, ""); w.Code != 401 {
		t.Fatal("old session valid")
	}
	if w := call(s, "POST", "/admin-api/auth/logout", "", rotated, ""); w.Code != 403 {
		t.Fatal("logout missing CSRF accepted")
	}
	w := call(s, "POST", "/admin-api/auth/logout", "", rotated, session.CSRFToken)
	if w.Code != 200 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout failed")
	}
	if w := call(s, "GET", "/admin-api/probe", "", rotated, ""); w.Code != 401 {
		t.Fatal("logout did not revoke")
	}
	cookie, _ = login(t, s, nil)
	s.admin.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if w := call(s, "GET", "/admin-api/probe", "", cookie, ""); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
	if len(s.admin.sessions) != 0 {
		t.Fatal("expired session not removed")
	}
}

func TestAdminOriginTransportAndMethods(t *testing.T) {
	s := testServer(t)
	cookie, session := login(t, s, nil)
	for _, origin := range []string{"", "null", "https://evil.test", "https://pool.test.evil.test", "http://pool.test"} {
		for _, target := range []string{"/admin-api/auth/login", "/admin-api/auth/logout"} {
			r := httptest.NewRequest("POST", "https://pool.test"+target, strings.NewReader(`{}`))
			r.Header.Set("Origin", origin)
			r.Header.Set("X-CSRF-Token", session.CSRFToken)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("origin %q on %s: %d", origin, target, w.Code)
			}
		}
	}
	for _, target := range []string{"/admin-api/auth/login", "/admin-api/auth/logout"} {
		if w := call(s, "GET", target, "", cookie, ""); w.Code != 405 {
			t.Fatalf("GET %s: %d", target, w.Code)
		}
	}
	if w := call(s, "POST", "/api/probe", "", nil, ""); w.Code != 405 {
		t.Fatal("public write accepted")
	}
	if w := call(s, "POST", "/admin-api/probe", "", cookie, session.CSRFToken); w.Code != 405 {
		t.Fatal("read-only admin write accepted")
	}
	for _, tc := range []struct {
		host, remote string
		local        bool
		want         int
	}{
		{"localhost:20000", "127.0.0.1:1234", false, 403},
		{"localhost:20000", "127.0.0.1:1234", true, 200},
		{"[::1]:20000", "[::1]:1234", true, 200},
		{"pool.test", "127.0.0.1:1234", true, 403},
		{"localhost:20000", "192.0.2.1:1234", true, 403},
	} {
		s.admin.localHTTP = tc.local
		r := httptest.NewRequest("GET", "http://"+tc.host+"/admin-api/auth/session", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("transport %+v: %d", tc, w.Code)
		}
	}
}

func TestAdminInvalidInputAndLoginLimits(t *testing.T) {
	s := testServer(t)
	for _, body := range []string{`{`, `{}`, `{"username":"operator","password":"a","extra":true}`, `{"username":"operator","password":"a"}{}`, `{"username":"operator","password":"` + strings.Repeat("a", 73) + `"}`, strings.Repeat("x", 5000)} {
		if w := call(s, "POST", "/admin-api/auth/login", body, nil, ""); w.Code != 400 {
			t.Fatalf("invalid body: %d", w.Code)
		}
	}
	for i := 0; i < 4; i++ {
		call(s, "POST", "/admin-api/auth/login", `{}`, nil, "")
	}
	w := call(s, "POST", "/admin-api/auth/login", `{}`, nil, "")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("login not throttled")
	}
	s.admin.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	if w := call(s, "POST", "/admin-api/auth/login", `{}`, nil, ""); w.Code != 400 {
		t.Fatal("rate limit did not expire")
	}
}

func TestAdminDisabledAndConfigurationRedaction(t *testing.T) {
	s := testServer(t)
	for _, target := range []string{"/config/api", "/api/config/api"} {
		w := call(s, "GET", target, "", nil, "")
		var data map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if len(data) != 2 || data["host"] != "127.0.0.1" || data["port"] != float64(20000) {
			t.Fatalf("private configuration leaked: %s", w.Body.String())
		}
	}
	s.admin = nil
	if w := call(s, "GET", "/admin-api/probe", "", nil, ""); w.Code != 503 {
		t.Fatal("disabled admin allowed access")
	}
	if w := call(s, "GET", "/api/probe", "", nil, ""); w.Code != 200 {
		t.Fatal("disabled admin blocks users")
	}
	// Every instance owns its handler; multiple pools/tests do not mutate DefaultServeMux.
	if _, err := NewServer(&config.Options{API: &config.APIOptions{}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, options := range []*config.APIOptions{
		{Admin: &config.AdminOptions{}},
		{Admin: &config.AdminOptions{Username: "operator", PasswordHashFile: "/missing-password-file"}},
		{TLS: &config.TLSServerOptions{}},
		{AllowedOrigins: []string{"*"}},
	} {
		if _, err := NewServer(&config.Options{API: options}, nil); err == nil {
			t.Fatalf("invalid config accepted: %+v", options)
		}
	}
}
