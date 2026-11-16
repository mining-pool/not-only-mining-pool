package pool

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/api"
	"github.com/mining-pool/not-only-mining-pool/config"
)

func (h *harness) settings() *SettingsDocument {
	h.t.Helper()
	doc, err := h.pool.Settings()
	if err != nil {
		h.t.Fatal(err)
	}
	return doc
}

func TestSettingsWriteTheFileAndApplyIt(t *testing.T) {
	h := newHarness(t)
	before, _ := os.ReadFile(h.path)
	doc := h.settings()
	if !doc.InForce || doc.Settings.Ports[h.portA].Diff != 8 || doc.Settings.API.Admin.Username != "operator" {
		t.Fatalf("settings: %+v", doc)
	}

	s := doc.Settings
	s.Ports[h.portB] = &config.PortOptions{Diff: 16}
	s.ConnectionTimeout = 1200
	report, err := h.pool.UpdateSettings(doc.Version, s, "operator@127.0.0.1")
	if err != nil || !report.OK || report.Actor != "operator@127.0.0.1" {
		t.Fatalf("update: %+v %v", report, err)
	}
	if !accepts(h.portB) || h.pool.Live.Get().ConnectionTimeout != 1200 {
		t.Fatal("settings not applied")
	}
	// Only what changed is reported (rewardRecipients went from absent to [],
	// which is no change).
	for _, c := range report.Changes {
		if strings.Contains(c, "rewardRecipients") {
			t.Fatalf("spurious change reported: %v", report.Changes)
		}
	}

	after, _ := os.ReadFile(h.path)
	written, err := DecodeConfig(after)
	if err != nil || written.Ports[h.portB].Diff != 16 || written.ConnectionTimeout != 1200 || written.Coin.Name != "Test" || written.Engine != "idle" {
		t.Fatalf("file not updated correctly: %s", after)
	}
	// Top-level keys keep their order; settings the editor does not own are untouched.
	keysBefore, _, _ := objectEntries(before)
	keysAfter, _, _ := objectEntries(after)
	if strings.Join(keysBefore, ",") != strings.Join(keysAfter, ",") {
		t.Fatalf("key order changed: %v -> %v", keysBefore, keysAfter)
	}
	if backup, _ := os.ReadFile(h.path + ".bak"); !bytes.Equal(backup, before) {
		t.Fatal("previous config not backed up")
	}
	audit, _ := os.ReadFile(h.path + ".audit.log")
	if !strings.Contains(string(audit), `"actor":"operator@127.0.0.1"`) || !strings.Contains(string(audit), "port "+strconv.Itoa(h.portB)+" added") {
		t.Fatalf("audit log: %s", audit)
	}
	if next := h.settings(); !next.InForce || next.Version == doc.Version {
		t.Fatal("version should change and the new file be in force")
	}
}

func TestSettingsRejectStaleOrInvalidChangesWithoutWriting(t *testing.T) {
	h := newHarness(t)
	doc := h.settings()

	// A manual edit on disk makes the editor's copy stale.
	h.set("connectionTimeout", 700)
	if h.settings().InForce {
		t.Fatal("unreloaded manual edit should not be reported as in force")
	}
	if _, err := h.pool.UpdateSettings(doc.Version, doc.Settings, "x"); !errors.Is(err, api.ErrSettingsConflict) {
		t.Fatalf("stale version: %v", err)
	}

	current := h.settings()
	edited, _ := os.ReadFile(h.path)
	for name, change := range map[string]func(s *Settings){
		"no ports":         func(s *Settings) { s.Ports = map[int]*config.PortOptions{} },
		"enabling API TLS": func(s *Settings) { s.API.TLS = &config.TLSServerOptions{CertFile: "a", KeyFile: "b"} },
		"fees at 100%":     func(s *Settings) { s.RewardRecipients = []*config.Recipient{{Address: "x", Percent: 100}} },
	} {
		fresh := h.settings().Settings
		change(fresh)
		report, err := h.pool.UpdateSettings(current.Version, fresh, "x")
		if err != nil || report.OK {
			t.Errorf("%s accepted: %+v %v", name, report, err)
		}
		if now, _ := os.ReadFile(h.path); !bytes.Equal(now, edited) {
			t.Fatalf("%s: rejected settings were written", name)
		}
	}
	if accepts(h.portB) || h.pool.Live.Get().ConnectionTimeout != 600 {
		t.Fatal("rejected settings changed the pool")
	}

	if _, err := h.pool.UpdateSettingsJSON(current.Version, json.RawMessage(`{"ports":{},"coin":{"name":"x"}}`), "x"); !errors.Is(err, api.ErrSettingsInvalid) {
		t.Fatalf("unknown field accepted: %v", err)
	}
	payment := h.settings().Settings
	payment.Payment = &PaymentSettings{Interval: 60}
	if _, err := h.pool.UpdateSettings(current.Version, payment, "x"); !errors.Is(err, api.ErrSettingsInvalid) {
		t.Fatalf("payment settings without a payment section: %v", err)
	}
}

func TestSettingsEndpoint(t *testing.T) {
	h := newHarness(t)
	if w := h.admin("GET", "/admin-api/settings", "", nil, ""); w.Code != 401 {
		t.Fatalf("anonymous read: %d", w.Code)
	}
	login := h.admin("POST", "/admin-api/auth/login", "", nil, `{"username":"operator","password":"`+adminPassword+`"}`)
	var session struct{ CSRFToken string }
	_ = json.Unmarshal(login.Body.Bytes(), &session)
	cookie := login.Result().Cookies()[0]

	w := h.admin("GET", "/admin-api/settings", "", cookie, "")
	var doc struct {
		Version  string          `json:"version"`
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil || w.Code != 200 || doc.Version == "" {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
	var s Settings
	_ = json.Unmarshal(doc.Settings, &s)
	s.ConnectionTimeout = 1500
	body := func(version string, s interface{}) string {
		b, _ := json.Marshal(map[string]interface{}{"version": version, "settings": s})
		return string(b)
	}

	if w := h.admin("PUT", "/admin-api/settings", "", cookie, body(doc.Version, s)); w.Code != 403 {
		t.Fatalf("write without CSRF: %d", w.Code)
	}
	if w := h.admin("PUT", "/admin-api/settings", session.CSRFToken, cookie, body("stale", s)); w.Code != http.StatusConflict {
		t.Fatalf("stale write: %d", w.Code)
	}
	bad := s
	bad.Ports = map[int]*config.PortOptions{}
	if w := h.admin("PUT", "/admin-api/settings", session.CSRFToken, cookie, body(doc.Version, bad)); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "port") {
		t.Fatalf("rejected write: %d %s", w.Code, w.Body.String())
	}
	if w := h.admin("PUT", "/admin-api/settings", session.CSRFToken, cookie, `{"version":"x"}`); w.Code != 400 {
		t.Fatalf("malformed write: %d", w.Code)
	}
	w = h.admin("PUT", "/admin-api/settings", session.CSRFToken, cookie, body(doc.Version, s))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"actor":"operator@`) || h.pool.Live.Get().ConnectionTimeout != 1500 {
		t.Fatalf("write: %d %s", w.Code, w.Body.String())
	}
	if w := h.admin("GET", "/api/settings", "", nil, ""); w.Code == 200 {
		t.Fatal("settings reachable on the public API")
	}
}
