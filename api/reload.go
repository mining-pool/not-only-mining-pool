package api

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mining-pool/not-only-mining-pool/config"
)

// originSet is the allow-list shared by CORS and the admin login, swapped as
// a whole on reload.
type originSet struct {
	m atomic.Pointer[map[string]bool]
}

func newOriginSet(origins map[string]bool) *originSet {
	o := &originSet{}
	o.set(origins)
	return o
}

func (o *originSet) get() map[string]bool { return *o.m.Load() }
func (o *originSet) set(m map[string]bool) {
	if m == nil {
		m = map[string]bool{}
	}
	o.m.Store(&m)
}

type hashrate struct {
	perDiff float64
	unit    string
}

// ReloadReport describes one configuration reload attempt.
type ReloadReport struct {
	At      time.Time `json:"at"`
	Trigger string    `json:"trigger"` // "signal" or "admin"
	OK      bool      `json:"ok"`
	Changes []string  `json:"changes"`
	Errors  []string  `json:"errors"`
}

// Reloader is implemented by the pool: it re-reads the configuration file and
// applies what can change at runtime.
type Reloader interface {
	Reload(trigger string) ReloadReport
	LastReload() *ReloadReport
}

// SetReloader enables the admin reload endpoints.
func (s *Server) SetReloader(r Reloader) {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.reloader = r
}

func (s *Server) currentReloader() Reloader {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	return s.reloader
}

// reloadFunc serves GET (last result) and POST (reload now) on
// /admin-api/reload. Only reachable through an admin session; POST also needs
// the CSRF token (enforced by adminAuth.handle).
func (s *Server) reloadFunc(w http.ResponseWriter, r *http.Request) {
	reloader := s.currentReloader()
	if reloader == nil {
		apiError(w, http.StatusNotImplemented, "reload_unavailable", "Configuration reload is not available on this server.")
		return
	}
	if r.Method == http.MethodPost {
		jsonResponse(w, http.StatusOK, reloader.Reload("admin"))
		return
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"last": reloader.LastReload()})
}

// PreparedAPI is a validated API settings change.
type PreparedAPI struct {
	s         *Server
	next      *config.APIOptions
	origins   map[string]bool
	admin     *adminAuth
	adminOpts *config.AdminOptions
	adminNew  bool
	cert      *tls.Certificate
	changes   []string
}

// PrepareAPI validates the reloadable API settings in next: allowed origins,
// administrator login, hashesPerDifficulty and the TLS certificate (re-read so
// renewals apply). host, port and enabling/disabling TLS need a restart and are
// rejected by the caller before this point.
func (s *Server) PrepareAPI(next *config.APIOptions) (*PreparedAPI, error) {
	if next == nil {
		return nil, errors.New("api configuration is required")
	}
	origins, err := parseOrigins(next.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	p := &PreparedAPI{s: s, next: next, origins: origins, adminOpts: next.Admin}
	if next.HashesPerDifficulty < 0 {
		return nil, errors.New("api.hashesPerDifficulty must not be negative")
	}
	if !reflect.DeepEqual(origins, s.origins.get()) {
		p.changes = append(p.changes, "api.allowedOrigins")
	}
	if !reflect.DeepEqual(next.Admin, s.adminOpts) {
		// Rebuilding the login invalidates every session, so only do it when
		// the administrator settings actually changed.
		admin, err := newAdminAuth(next.Admin, s.origins)
		if err != nil {
			return nil, err
		}
		p.admin, p.adminNew = admin, true
		p.changes = append(p.changes, "api.admin (all administrator sessions signed out)")
	}
	if next.HashesPerDifficulty != s.apiConfig().HashesPerDifficulty {
		p.changes = append(p.changes, "api.hashesPerDifficulty")
	}
	if next.TLS != nil && s.certs != nil {
		cert, err := next.TLS.LoadCertificate()
		if err != nil {
			return nil, fmt.Errorf("api TLS: %w", err)
		}
		p.cert = cert
	}
	return p, nil
}

// Commit applies the prepared API settings.
func (p *PreparedAPI) Commit() []string {
	s := p.s
	s.origins.set(p.origins)
	if p.adminNew {
		s.adminPtr.Store(p.admin)
		s.adminOpts = p.adminOpts
	}
	if p.cert != nil {
		s.certs.Set(p.cert)
	}
	s.apiConf.Store(p.next)
	s.applyHashrate()
	return p.changes
}

// UpdatePorts refreshes the public port list after a reload.
func (s *Server) UpdatePorts(ports map[int]*config.PortOptions) {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	next := map[string]interface{}{}
	for k, v := range s.publicConfig() {
		next[k] = v
	}
	next["ports"] = publicPorts(ports)
	s.conf.Store(&next)
}

// reloadState groups the fields that a reload replaces.
type reloadState struct {
	reloadMu  sync.Mutex
	reloader  Reloader
	origins   *originSet
	adminPtr  atomic.Pointer[adminAuth]
	adminOpts *config.AdminOptions
	conf      atomic.Pointer[map[string]interface{}]
	rate      atomic.Pointer[hashrate]
	apiConf   atomic.Pointer[config.APIOptions]
	certs     *config.CertificateHolder

	// engine-declared work per difficulty, before any config override
	basePerDiff float64
	baseUnit    string
}

func (s *Server) admin() *adminAuth                    { return s.adminPtr.Load() }
func (s *Server) apiConfig() *config.APIOptions        { return s.apiConf.Load() }
func (s *Server) publicConfig() map[string]interface{} { return *s.conf.Load() }

func (s *Server) applyHashrate() {
	h := hashrate{perDiff: s.basePerDiff, unit: s.baseUnit}
	if o := s.apiConfig().HashesPerDifficulty; o > 0 {
		h.perDiff = o
	}
	if h.perDiff <= 0 {
		h.perDiff = 1
	}
	if h.unit == "" {
		h.unit = "H/s"
	}
	s.rate.Store(&h)
}
