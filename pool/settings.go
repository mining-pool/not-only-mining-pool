package pool

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mining-pool/not-only-mining-pool/api"
	"github.com/mining-pool/not-only-mining-pool/config"
)

// Settings is the part of config.json the admin API may edit: exactly the
// settings a reload can apply. Everything else in the file is preserved.
type Settings struct {
	Ports             map[int]*config.PortOptions `json:"ports"`
	Banning           *config.BanningOptions      `json:"banning"`
	ConnectionTimeout int                         `json:"connectionTimeout"`
	TCPProxyProtocol  bool                        `json:"tcpProxyProtocol"`
	RewardRecipients  []*config.Recipient         `json:"rewardRecipients"`
	// Present only when the file has a payment section.
	Payment *PaymentSettings `json:"payment,omitempty"`
	// Present only when the file has quantus.payments.
	QuantusPayments *QuantusPaymentSettings `json:"quantusPayments,omitempty"`
	API             APISettings             `json:"api"`
}

type PaymentSettings struct {
	Interval         int64   `json:"interval"`
	MinPayment       float64 `json:"minPayment"`
	MinConfirmations int64   `json:"minConfirmations"`
}

type QuantusPaymentSettings struct {
	MinPaymentUnits string `json:"minPaymentUnits"`
	ReserveUnits    string `json:"reserveUnits"`
}

type APISettings struct {
	AllowedOrigins      []string                 `json:"allowedOrigins"`
	HashesPerDifficulty float64                  `json:"hashesPerDifficulty"`
	Admin               *config.AdminOptions     `json:"admin"`
	TLS                 *config.TLSServerOptions `json:"tls"` // paths only; switching TLS on/off needs a restart
}

func settingsOf(o *config.Options) *Settings {
	s := &Settings{
		Ports: o.Ports, Banning: o.Banning, ConnectionTimeout: o.ConnectionTimeout,
		TCPProxyProtocol: o.TCPProxyProtocol, RewardRecipients: o.RewardRecipients,
	}
	if s.RewardRecipients == nil {
		s.RewardRecipients = []*config.Recipient{}
	}
	if o.PaymentOptions != nil {
		s.Payment = &PaymentSettings{o.PaymentOptions.Interval, o.PaymentOptions.MinPayment, o.PaymentOptions.MinConfirmations}
	}
	if o.Quantus != nil && o.Quantus.Payments != nil {
		s.QuantusPayments = &QuantusPaymentSettings{o.Quantus.Payments.MinPaymentUnits, o.Quantus.Payments.ReserveUnits}
	}
	if o.API != nil {
		s.API = APISettings{o.API.AllowedOrigins, o.API.HashesPerDifficulty, o.API.Admin, o.API.TLS}
	}
	if s.API.AllowedOrigins == nil {
		s.API.AllowedOrigins = []string{}
	}
	return s
}

// SettingsDocument is what GET /admin-api/settings returns.
type SettingsDocument struct {
	Version  string    `json:"version"`
	InForce  bool      `json:"inForce"` // the file on disk is the configuration running now
	Settings *Settings `json:"settings"`
}

// SettingsJSON implements api.SettingsEditor.
func (p *Pool) SettingsJSON() (interface{}, error) { return p.Settings() }

// UpdateSettingsJSON implements api.SettingsEditor. Unknown fields are
// rejected so a typo cannot be silently ignored.
func (p *Pool) UpdateSettingsJSON(version string, raw json.RawMessage, actor string) (api.ReloadReport, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Settings
	if err := dec.Decode(&s); err != nil {
		return api.ReloadReport{}, fmt.Errorf("%w: %v", api.ErrSettingsInvalid, err)
	}
	return p.UpdateSettings(version, &s, actor)
}

// Settings returns the editable settings from config.json on disk.
func (p *Pool) Settings() (*SettingsDocument, error) {
	p.reload.mu.Lock()
	defer p.reload.mu.Unlock()
	if p.reload.path == "" {
		return nil, errors.New("reload is not enabled")
	}
	raw, err := os.ReadFile(p.reload.path)
	if err != nil {
		return nil, err
	}
	file, err := DecodeConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.reload.path, err)
	}
	version := fileVersion(raw)
	return &SettingsDocument{Version: version, InForce: version == p.reload.applied, Settings: settingsOf(file)}, nil
}

// UpdateSettings writes new settings into config.json and applies them, as
// one change: the file is only written once the whole new configuration has
// validated, and nothing is applied if writing fails. version must match the
// file on disk, so concurrent edits (including manual ones) are not lost.
func (p *Pool) UpdateSettings(version string, s *Settings, actor string) (api.ReloadReport, error) {
	p.reload.mu.Lock()
	defer p.reload.mu.Unlock()
	if p.reload.path == "" {
		return api.ReloadReport{}, errors.New("reload is not enabled")
	}
	raw, err := os.ReadFile(p.reload.path)
	if err != nil {
		return api.ReloadReport{}, err
	}
	if version != fileVersion(raw) {
		return api.ReloadReport{}, api.ErrSettingsConflict
	}
	file, err := DecodeConfig(raw)
	if err != nil {
		return api.ReloadReport{}, fmt.Errorf("%s: %w", p.reload.path, err)
	}
	candidate, err := patchSettings(raw, file, s)
	if err != nil {
		return api.ReloadReport{}, fmt.Errorf("%w: %v", api.ErrSettingsInvalid, err)
	}
	report := p.applyRaw(candidate, "settings", actor, func() error { return writeConfig(p.reload.path, raw, candidate) })
	p.audit(report)
	return report, nil
}

// patchSettings replaces the editable settings in raw, keeping every other
// key and the order of keys.
func patchSettings(raw []byte, file *config.Options, s *Settings) ([]byte, error) {
	if s == nil {
		return nil, errors.New("settings are required")
	}
	type edit struct {
		path  []string
		value interface{}
	}
	edits := []edit{
		{[]string{"ports"}, portsJSON(s.Ports)},
		{[]string{"banning"}, s.Banning},
		{[]string{"connectionTimeout"}, s.ConnectionTimeout},
		{[]string{"tcpProxyProtocol"}, s.TCPProxyProtocol},
		{[]string{"rewardRecipients"}, s.RewardRecipients},
		{[]string{"api", "allowedOrigins"}, s.API.AllowedOrigins},
		{[]string{"api", "hashesPerDifficulty"}, s.API.HashesPerDifficulty},
		{[]string{"api", "admin"}, s.API.Admin},
		{[]string{"api", "tls"}, s.API.TLS},
	}
	switch {
	case s.Payment != nil && file.PaymentOptions == nil:
		return nil, errors.New("payment settings need a payment section in config.json (restart required)")
	case s.Payment != nil:
		edits = append(edits,
			edit{[]string{"payment", "interval"}, s.Payment.Interval},
			edit{[]string{"payment", "minPayment"}, s.Payment.MinPayment},
			edit{[]string{"payment", "minConfirmations"}, s.Payment.MinConfirmations})
	}
	switch {
	case s.QuantusPayments != nil && (file.Quantus == nil || file.Quantus.Payments == nil):
		return nil, errors.New("quantus payment settings need quantus.payments in config.json (restart required)")
	case s.QuantusPayments != nil:
		edits = append(edits,
			edit{[]string{"quantus", "payments", "minPaymentUnits"}, s.QuantusPayments.MinPaymentUnits},
			edit{[]string{"quantus", "payments", "reserveUnits"}, s.QuantusPayments.ReserveUnits})
	}
	doc := json.RawMessage(raw)
	for _, e := range edits {
		value, err := json.Marshal(e.value)
		if err != nil {
			return nil, err
		}
		// Do not add keys the operator never wrote when the value is the
		// zero value: decoding treats a missing key exactly the same way.
		if zeroJSON(value) && !hasJSON(doc, e.path) {
			continue
		}
		if doc, err = setJSON(doc, e.path, value); err != nil {
			return nil, err
		}
	}
	var out bytes.Buffer
	if err := json.Indent(&out, doc, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// portsJSON writes port numbers as JSON object keys.
func portsJSON(ports map[int]*config.PortOptions) map[string]*config.PortOptions {
	out := make(map[string]*config.PortOptions, len(ports))
	for port, opts := range ports {
		out[strconv.Itoa(port)] = opts
	}
	return out
}

// setJSON sets the value at path in a JSON object, creating objects along
// the way and keeping the order of existing keys.
func setJSON(doc json.RawMessage, path []string, value json.RawMessage) (json.RawMessage, error) {
	if len(path) == 0 {
		return value, nil
	}
	keys, values, err := objectEntries(doc)
	if err != nil {
		return nil, err
	}
	i := -1
	for j, k := range keys {
		if k == path[0] {
			i = j
		}
	}
	child := json.RawMessage("{}")
	if i >= 0 && len(path) > 1 && !bytes.Equal(bytes.TrimSpace(values[i]), []byte("null")) {
		child = values[i]
	}
	updated, err := setJSON(child, path[1:], value)
	if err != nil {
		return nil, err
	}
	if i < 0 {
		keys, values = append(keys, path[0]), append(values, updated)
	} else {
		values[i] = updated
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for j, k := range keys {
		if j > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(k)
		b.Write(name)
		b.WriteByte(':')
		b.Write(values[j])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func zeroJSON(v json.RawMessage) bool {
	switch string(v) {
	case "null", "[]", "{}", "false", "0", `""`:
		return true
	}
	return false
}

// hasJSON reports whether path exists in doc.
func hasJSON(doc json.RawMessage, path []string) bool {
	for _, key := range path {
		keys, values, err := objectEntries(doc)
		if err != nil {
			return false
		}
		found := false
		for i, k := range keys {
			if k == key {
				doc, found = values[i], true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func objectEntries(doc json.RawMessage) ([]string, []json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, nil, errors.New("configuration must be a JSON object")
	}
	var keys []string
	var values []json.RawMessage
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, nil, err
		}
		keys, values = append(keys, tok.(string)), append(values, value)
	}
	return keys, values, nil
}

// writeConfig replaces the file atomically: the previous content goes to
// path.bak, the new content to a temporary file that is synced and renamed.
func writeConfig(path string, previous, next []byte) error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path+".bak", previous, 0o600); err != nil {
		return fmt.Errorf("back up config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(next); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// audit appends one JSON line per settings change attempt to
// config.json.audit.log next to the configuration file.
func (p *Pool) audit(report api.ReloadReport) {
	f, err := os.OpenFile(p.reload.path+".audit.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Error("settings audit log: ", err)
		return
	}
	defer f.Close()
	line, _ := json.Marshal(struct {
		At      time.Time `json:"at"`
		Actor   string    `json:"actor"`
		OK      bool      `json:"ok"`
		Changes []string  `json:"changes"`
		Errors  []string  `json:"errors"`
	}{report.At, report.Actor, report.OK, report.Changes, report.Errors})
	_, _ = f.Write(append(line, '\n'))
}
