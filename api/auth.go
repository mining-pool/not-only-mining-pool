package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mining-pool/not-only-mining-pool/config"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "nomp_admin_session"

type adminSession struct {
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expiresAt"`
	CSRFToken string    `json:"csrfToken"`
}

type loginWindow struct {
	until time.Time
	count int
}

// Sessions are process-local, bounded and revoked on restart. Only token hashes
// are retained server-side; credentials and cookies are never logged.
type adminAuth struct {
	username   string
	hash       []byte
	ttl        time.Duration
	localHTTP  bool
	sameSite   http.SameSite
	origins    map[string]bool
	mu         sync.Mutex
	sessions   map[[32]byte]adminSession
	attempts   map[string]loginWindow
	loginSlots chan struct{}
	now        func() time.Time
}

func newAdminAuth(options *config.AdminOptions, origins map[string]bool) (*adminAuth, error) {
	if options == nil {
		return nil, nil // disabled, never an unauthenticated admin mode
	}
	if strings.TrimSpace(options.Username) == "" || len(options.Username) > 128 || options.PasswordHashFile == "" {
		return nil, errors.New("api.admin requires username and passwordHashFile")
	}
	file, err := os.Open(options.PasswordHashFile)
	if err != nil {
		return nil, fmt.Errorf("read administrator password hash: %w", err)
	}
	defer file.Close()
	hash, err := io.ReadAll(io.LimitReader(file, 256))
	if err != nil {
		return nil, errors.New("cannot read administrator password hash")
	}
	hash = []byte(strings.TrimSpace(string(hash)))
	cost, err := bcrypt.Cost(hash)
	if err != nil || len(hash) != 60 || cost < 10 || cost > 16 {
		return nil, errors.New("administrator password file must contain a bcrypt hash with cost 10–16")
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte("hash-format-validation")); err != nil && !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return nil, errors.New("administrator password hash is malformed")
	}
	seconds := options.SessionTTLSeconds
	if seconds == 0 {
		seconds = 8 * 60 * 60
	}
	if seconds < 60 || seconds > 86400 {
		return nil, errors.New("admin sessionTTLSeconds must be between 60 and 86400")
	}
	sameSite := http.SameSiteStrictMode
	switch options.CookieSameSite {
	case "", "strict":
	case "none":
		if options.AllowInsecureHTTP {
			return nil, errors.New("cookieSameSite=none requires HTTPS; disable allowInsecureHTTP")
		}
		sameSite = http.SameSiteNoneMode
	default:
		return nil, errors.New("cookieSameSite must be strict or none")
	}
	return &adminAuth{
		username: options.Username, hash: hash, ttl: time.Duration(seconds) * time.Second,
		sameSite: sameSite, origins: origins,
		localHTTP: options.AllowInsecureHTTP, sessions: make(map[[32]byte]adminSession),
		attempts: make(map[string]loginWindow), loginSlots: make(chan struct{}, 4), now: time.Now,
	}, nil
}

func jsonResponse(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func apiError(w http.ResponseWriter, status int, code, message string) {
	jsonResponse(w, status, map[string]string{"code": code, "message": message})
}

func (a *adminAuth) secureTransport(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	peerIP, hostIP := net.ParseIP(peer), net.ParseIP(host)
	return a.localHTTP && peerIP != nil && peerIP.IsLoopback() &&
		(host == "localhost" || (hostIP != nil && hostIP.IsLoopback()))
}

// Do not trust X-Forwarded-* headers. Vite forwards the original Host during
// local development; production TLS terminates in this Go server.
func sameOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && origin.Scheme == scheme && strings.EqualFold(origin.Host, r.Host) &&
		origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == ""
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func tokenKey(token string) [32]byte { return sha256.Sum256([]byte(token)) }

func (a *adminAuth) allowLogin(r *http.Request) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for key, window := range a.attempts {
		if !now.Before(window.until) {
			delete(a.attempts, key)
		}
	}
	window, exists := a.attempts[ip]
	if !exists {
		if len(a.attempts) >= 1024 {
			return false
		}
		window.until = now.Add(5 * time.Minute)
	}
	if window.count >= 10 {
		return false
	}
	window.count++
	a.attempts[ip] = window
	return true
}

func (a *adminAuth) lookup(r *http.Request) (adminSession, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) != 43 {
		return adminSession{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := tokenKey(cookie.Value)
	session, ok := a.sessions[key]
	if ok && !a.now().Before(session.ExpiresAt) {
		delete(a.sessions, key)
		ok = false
	}
	return session, ok
}

func (a *adminAuth) setCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/admin-api", HttpOnly: true,
		Secure: r.TLS != nil, SameSite: a.sameSite, MaxAge: maxAge, Expires: expires,
	})
}

func (a *adminAuth) handle(w http.ResponseWriter, r *http.Request, public http.Handler) {
	if a == nil {
		apiError(w, http.StatusServiceUnavailable, "admin_disabled", "Administrator login is not configured; set api.admin.")
		return
	}
	if !a.secureTransport(r) {
		apiError(w, http.StatusForbidden, "https_required", "Administrator login requires HTTPS; enable allowInsecureHTTP only for local development.")
		return
	}
	if r.URL.Path == "/auth/login" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			apiError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST to log in.")
			return
		}
		if !allowedOrigin(r, a.origins) {
			apiError(w, http.StatusForbidden, "origin_mismatch", "Request origin does not match.")
			return
		}
		a.login(w, r)
		return
	}
	session, ok := a.lookup(r)
	if !ok {
		apiError(w, http.StatusUnauthorized, "session_expired", "Administrator login required.")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if !allowedOrigin(r, a.origins) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRFToken)) != 1 {
			apiError(w, http.StatusForbidden, "csrf_invalid", "Session check failed; refresh and try again.")
			return
		}
	}
	switch r.URL.Path {
	case "/auth/session":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			apiError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET to read the session.")
			return
		}
		jsonResponse(w, http.StatusOK, session)
	case "/auth/logout":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			apiError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST to log out.")
			return
		}
		cookie, _ := r.Cookie(sessionCookie)
		a.mu.Lock()
		delete(a.sessions, tokenKey(cookie.Value))
		a.mu.Unlock()
		a.setCookie(w, r, "", time.Unix(1, 0), -1)
		jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		public.ServeHTTP(w, withAdmin(r))
	}
}

func (a *adminAuth) login(w http.ResponseWriter, r *http.Request) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		apiError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Login requests must use JSON.")
		return
	}
	if !a.allowLogin(r) {
		w.Header().Set("Retry-After", "300")
		apiError(w, http.StatusTooManyRequests, "login_limited", "Too many login attempts; try again in 5 minutes.")
		return
	}
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	default:
		apiError(w, http.StatusTooManyRequests, "login_busy", "Login service is busy; try again later.")
		return
	}
	var credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		len(credentials.Username) > 128 || len(credentials.Password) == 0 || len(credentials.Password) > 72 {
		apiError(w, http.StatusBadRequest, "invalid_credentials", "Malformed login request.")
		return
	}
	// Perform the password check even for a wrong username.
	passwordErr := bcrypt.CompareHashAndPassword(a.hash, []byte(credentials.Password))
	if passwordErr != nil || subtle.ConstantTimeCompare([]byte(credentials.Username), []byte(a.username)) != 1 {
		apiError(w, http.StatusUnauthorized, "invalid_credentials", "Incorrect username or password.")
		return
	}
	token, err := randomToken()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "session_failed", "Could not create a session.")
		return
	}
	csrf, err := randomToken()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "session_failed", "Could not create a session.")
		return
	}
	a.mu.Lock()
	now := a.now()
	for key, session := range a.sessions {
		if !now.Before(session.ExpiresAt) {
			delete(a.sessions, key)
		}
	}
	if old, err := r.Cookie(sessionCookie); err == nil {
		delete(a.sessions, tokenKey(old.Value))
	}
	if len(a.sessions) >= 256 {
		a.mu.Unlock()
		apiError(w, http.StatusServiceUnavailable, "session_limit", "Too many active sessions; try again later.")
		return
	}
	session := adminSession{Username: a.username, ExpiresAt: now.Add(a.ttl), CSRFToken: csrf}
	a.sessions[tokenKey(token)] = session
	a.mu.Unlock()
	a.setCookie(w, r, token, session.ExpiresAt, int(a.ttl.Seconds()))
	jsonResponse(w, http.StatusOK, session)
}
