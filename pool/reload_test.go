package pool

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/mining-pool/not-only-mining-pool/api"
	"github.com/mining-pool/not-only-mining-pool/bans"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/stratum"
	"github.com/mining-pool/not-only-mining-pool/types"
)

// idleEngine satisfies engine.Engine without a node; reload tests only need
// the stratum listeners.
type idleEngine struct{ stop chan struct{} }

func (e *idleEngine) Name() string               { return "idle" }
func (e *idleEngine) Init(*config.Options) error { return nil }
func (e *idleEngine) Hashrate() engine.HashrateUnit {
	return engine.HashrateUnit{PerDiff: 1, Unit: "H/s"}
}
func (e *idleEngine) JobNotification(bool) (string, []interface{}) {
	return "mining.notify", nil
}
func (e *idleEngine) OnSubscribe(engine.Session, []interface{}) (interface{}, []byte, int) {
	return true, nil, 0
}
func (e *idleEngine) OnSubmit(engine.Session, []interface{}) *types.Share { return &types.Share{} }
func (e *idleEngine) Watch(func()) error                                  { <-e.stop; return nil }

const adminPassword = "reload-test-password"

type harness struct {
	t     *testing.T
	pool  *Pool
	path  string
	doc   map[string]interface{}
	portA int
	portB int
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// newHarness starts a pool (stratum + API, no node) from a real config file.
func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	hash, _ := bcrypt.GenerateFromPassword([]byte(adminPassword), 10)
	hashFile := filepath.Join(dir, "admin.hash")
	if err := os.WriteFile(hashFile, hash, 0o600); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, path: filepath.Join(dir, "config.json"), portA: freePort(t), portB: freePort(t)}
	h.doc = map[string]interface{}{
		"engine":            "idle",
		"coin":              map[string]interface{}{"name": "Test", "symbol": "TST"},
		"algorithm":         map[string]interface{}{"name": "sha256d"},
		"connectionTimeout": 600,
		"ports":             map[string]interface{}{strconv.Itoa(h.portA): map[string]interface{}{"diff": 8}},
		"banning":           map[string]interface{}{"time": 600, "invalidPercent": 50, "checkThreshold": 500, "purgeInterval": 300},
		"api": map[string]interface{}{
			"host": "127.0.0.1", "port": freePort(t),
			"allowedOrigins": []string{"https://pool.test"},
			"admin":          map[string]interface{}{"username": "operator", "passwordHashFile": hashFile, "sessionTTLSeconds": 600},
		},
	}
	raw := h.write()

	options, err := DecodeConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	eng := &idleEngine{stop: make(chan struct{})}
	t.Cleanup(func() { close(eng.stop) })
	server, err := api.NewServer(options, nil)
	if err != nil {
		t.Fatal(err)
	}
	live := config.NewLive(options)
	ss := stratum.NewStratumServer(options, nil, bans.NewBanningManager(options.Banning))
	ss.Live = live
	ss.Engine = eng
	h.pool = &Pool{Options: options, Live: live, Engine: eng, APIServer: server, StratumServer: ss, Stats: NewStats()}
	h.pool.StartStratumServer()
	if err := h.pool.EnableReload(h.path, raw); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) write() []byte {
	raw, err := json.Marshal(h.doc)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(h.path, raw, 0o600); err != nil {
		h.t.Fatal(err)
	}
	return raw
}

func (h *harness) set(path string, value interface{}) {
	parts := strings.Split(path, ".")
	node := h.doc
	for _, p := range parts[:len(parts)-1] {
		node = node[p].(map[string]interface{})
	}
	node[parts[len(parts)-1]] = value
	h.write()
}

func accepts(port int) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func TestReloadRejectsSettingsThatNeedARestart(t *testing.T) {
	h := newHarness(t)
	before := h.pool.Live.Get()
	h.set("coin.name", "Other")
	h.set("ports", map[string]interface{}{strconv.Itoa(h.portB): map[string]interface{}{"diff": 1}})

	r := h.pool.Reload("signal")
	if r.OK || len(r.Errors) != 1 || !strings.Contains(r.Errors[0], "coin") {
		t.Fatalf("report: %+v", r)
	}
	if h.pool.Live.Get() != before || accepts(h.portB) || !accepts(h.portA) {
		t.Fatal("a rejected reload changed the running pool")
	}
	if last := h.pool.LastReload(); last == nil || last.OK || last.Trigger != "signal" {
		t.Fatalf("last reload not recorded: %+v", last)
	}
}

func TestReloadIsAllOrNothing(t *testing.T) {
	h := newHarness(t)
	h.set("ports", map[string]interface{}{
		strconv.Itoa(h.portA): map[string]interface{}{"diff": 8},
		strconv.Itoa(h.portB): map[string]interface{}{"diff": 1},
	})
	bad := filepath.Join(t.TempDir(), "bad.hash")
	_ = os.WriteFile(bad, []byte("not a bcrypt hash"), 0o600)
	h.set("api.admin.passwordHashFile", bad)

	if r := h.pool.Reload("admin"); r.OK || !strings.Contains(strings.Join(r.Errors, " "), "bcrypt") {
		t.Fatalf("report: %+v", r)
	}
	if accepts(h.portB) {
		t.Fatal("a port from a rejected reload is still bound")
	}
	if got := h.pool.StratumServer.Ports(); len(got) != 1 || got[0] != h.portA {
		t.Fatalf("serving %v", got)
	}
}

func TestReloadAppliesRuntimeSettings(t *testing.T) {
	h := newHarness(t)
	h.set("ports", map[string]interface{}{strconv.Itoa(h.portB): map[string]interface{}{"diff": 64}})
	h.set("banning.invalidPercent", 25.0)
	h.set("connectionTimeout", 900)
	h.set("api.allowedOrigins", []string{"https://pool.test", "https://new.pool.test"})

	r := h.pool.Reload("signal")
	if !r.OK {
		t.Fatalf("report: %+v", r)
	}
	joined := strings.Join(r.Changes, "; ")
	for _, want := range []string{"port " + strconv.Itoa(h.portA) + " removed", "port " + strconv.Itoa(h.portB) + " added", "banning", "connectionTimeout", "api.allowedOrigins"} {
		if !strings.Contains(joined, want) {
			t.Errorf("changes %q missing %q", joined, want)
		}
	}
	if accepts(h.portA) || !accepts(h.portB) {
		t.Fatal("ports not switched")
	}
	live := h.pool.Live.Get()
	if live.ConnectionTimeout != 900 || live.Ports[h.portB].Diff != 64 || live.Coin.Name != "Test" {
		t.Fatalf("live config: %+v", live)
	}
	if b := h.pool.StratumServer.BanningManager.Options(); b == nil || b.InvalidPercent != 25 {
		t.Fatalf("banning not updated: %+v", b)
	}
	if len(h.pool.Stats.StratumPorts) != 1 || h.pool.Stats.StratumPorts[0] != h.portB {
		t.Fatalf("stats ports %v", h.pool.Stats.StratumPorts)
	}

	// The public API reflects the new ports and the new allowed origin.
	req := httptest.NewRequest("GET", "/api/config/ports", nil)
	req.Header.Set("Origin", "https://new.pool.test")
	w := httptest.NewRecorder()
	h.pool.APIServer.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"`+strconv.Itoa(h.portB)+`"`) || strings.Contains(w.Body.String(), `"`+strconv.Itoa(h.portA)+`"`) {
		t.Fatalf("public ports after reload: %d %s", w.Code, w.Body.String())
	}

	if r := h.pool.Reload("signal"); !r.OK || len(r.Changes) != 1 || r.Changes[0] != "no changes" {
		t.Fatalf("unchanged reload: %+v", r)
	}
}

func (h *harness) admin(method, path, csrf string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://api.test"+path, strings.NewReader(body))
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Origin", "https://pool.test")
	r.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.pool.APIServer.ServeHTTP(w, r)
	return w
}

func TestAdminReloadEndpoint(t *testing.T) {
	h := newHarness(t)
	if w := h.admin("POST", "/admin-api/reload", "", nil, ""); w.Code != 401 {
		t.Fatalf("anonymous reload: %d", w.Code)
	}
	if w := h.admin("POST", "/api/reload", "", nil, ""); w.Code == 200 {
		t.Fatal("reload reachable on the public API")
	}
	login := h.admin("POST", "/admin-api/auth/login", "", nil, `{"username":"operator","password":"`+adminPassword+`"}`)
	if login.Code != 200 {
		t.Fatalf("login: %s", login.Body.String())
	}
	var session struct{ CSRFToken string }
	_ = json.Unmarshal(login.Body.Bytes(), &session)
	cookie := login.Result().Cookies()[0]

	if w := h.admin("POST", "/admin-api/reload", "", cookie, ""); w.Code != 403 {
		t.Fatalf("reload without CSRF token: %d", w.Code)
	}
	h.set("connectionTimeout", 1200)
	w := h.admin("POST", "/admin-api/reload", session.CSRFToken, cookie, "")
	var report api.ReloadReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil || w.Code != 200 || !report.OK || report.Trigger != "admin" {
		t.Fatalf("reload: %d %s", w.Code, w.Body.String())
	}
	w = h.admin("GET", "/admin-api/reload", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "connectionTimeout") {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
}
