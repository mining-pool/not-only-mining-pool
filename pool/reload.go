package pool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/mining-pool/not-only-mining-pool/api"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/engine"
)

// reloadState remembers the configuration file as last applied (before any
// runtime enrichment such as coin.testnet) so a reload can tell which
// settings the operator changed.
type reloadState struct {
	mu      sync.Mutex
	path    string
	file    *config.Options
	applied string // version of the file content in force
	last    *api.ReloadReport
}

// fileVersion identifies a configuration file's content.
func fileVersion(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// DecodeConfig parses a configuration file.
func DecodeConfig(raw []byte) (*config.Options, error) {
	var o config.Options
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&o); err != nil {
		return nil, err
	}
	return &o, nil
}

// EnableReload lets Reload re-read path. raw is the file content the pool was
// started with.
func (p *Pool) EnableReload(path string, raw []byte) error {
	file, err := DecodeConfig(raw)
	if err != nil {
		return err
	}
	p.reload.mu.Lock()
	p.reload.path, p.reload.file, p.reload.applied = path, file, fileVersion(raw)
	p.reload.mu.Unlock()
	p.APIServer.SetReloader(p)
	return nil
}

// LastReload returns the most recent reload attempt, if any.
func (p *Pool) LastReload() *api.ReloadReport {
	p.reload.mu.Lock()
	defer p.reload.mu.Unlock()
	if p.reload.last == nil {
		return nil
	}
	r := *p.reload.last
	return &r
}

// restartOnly returns the settings that cannot change while running, keyed
// by their config name. Reloadable fields are blanked so they never count as a
// difference.
func restartOnly(o *config.Options) map[string]interface{} {
	out := map[string]interface{}{
		"disablePayment": o.DisablePayment,
		"coin":           o.Coin,
		"engine":         o.Engine,
		"quantus (except payments.minPaymentUnits, reserveUnits)": quantusRestartOnly(o.Quantus),
		"poolAddress":            o.PoolAddress,
		"blockRefreshInterval":   o.BlockRefreshInterval,
		"jobRebroadcastTimeout":  o.JobRebroadcastTimeout,
		"emitInvalidBlockHashes": o.EmitInvalidBlockHashes,
		"daemons":                o.Daemons,
		"p2p":                    o.P2P,
		"storage":                o.Storage,
		"algorithm":              o.Algorithm,
	}
	if o.API != nil {
		out["api.host"] = o.API.Host
		out["api.port"] = o.API.Port
		out["api.tls (enabled)"] = o.API.TLS != nil
	}
	if o.PaymentOptions != nil {
		pay := *o.PaymentOptions
		pay.Interval, pay.MinPayment, pay.MinConfirmations = 0, 0, 0
		out["payment (except interval, minPayment, minConfirmations)"] = pay
	} else {
		out["payment (except interval, minPayment, minConfirmations)"] = nil
	}
	return out
}

// quantusRestartOnly blanks the Quantus payout thresholds, which the engine
// reloads itself.
func quantusRestartOnly(q *config.QuantusOptions) *config.QuantusOptions {
	if q == nil || q.Payments == nil {
		return q
	}
	copied, payments := *q, *q.Payments
	payments.MinPaymentUnits, payments.ReserveUnits = "", ""
	copied.Payments = &payments
	return &copied
}

func restartRequired(running, next *config.Options) []string {
	a, b := restartOnly(running), restartOnly(next)
	var changed []string
	for key, value := range a {
		x, _ := json.Marshal(value)
		y, _ := json.Marshal(b[key])
		if !bytes.Equal(x, y) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}

func validateReloadable(next *config.Options, gbt bool) error {
	if b := next.Banning; b != nil {
		if b.Time <= 0 || b.PurgeInterval < 0 || b.InvalidPercent <= 0 || b.InvalidPercent > 100 {
			return errors.New("banning: time must be positive and invalidPercent within (0, 100]")
		}
	}
	if next.ConnectionTimeout < 0 {
		return errors.New("connectionTimeout must not be negative")
	}
	if err := config.ValidateRecipients(next.RewardRecipients, gbt); err != nil {
		return err
	}
	if pay := next.PaymentOptions; pay != nil && (pay.Interval < 0 || pay.MinPayment < 0 || pay.MinConfirmations < 0) {
		return errors.New("payment: interval, minPayment and minConfirmations must not be negative")
	}
	return nil
}

// Reload re-reads the configuration file and applies every setting that can
// change at runtime. Either all of it applies or, on any validation error,
// nothing does and the pool keeps running unchanged.
func (p *Pool) Reload(trigger, actor string) api.ReloadReport {
	p.reload.mu.Lock()
	defer p.reload.mu.Unlock()
	if p.reload.path == "" {
		return p.record(api.ReloadReport{At: time.Now().UTC(), Trigger: trigger, Actor: actor, Changes: []string{}, Errors: []string{"reload is not enabled"}})
	}
	raw, err := os.ReadFile(p.reload.path)
	if err != nil {
		return p.record(api.ReloadReport{At: time.Now().UTC(), Trigger: trigger, Actor: actor, Changes: []string{}, Errors: []string{err.Error()}})
	}
	return p.applyRaw(raw, trigger, actor, nil)
}

// record stores and logs a finished attempt.
func (p *Pool) record(report api.ReloadReport) api.ReloadReport {
	r := report
	p.reload.last = &r
	if report.OK {
		log.Warnf("configuration reloaded (%s%s): %v", report.Trigger, by(report.Actor), report.Changes)
	} else {
		log.Errorf("configuration reload (%s%s) rejected: %v", report.Trigger, by(report.Actor), report.Errors)
	}
	return report
}

func by(actor string) string {
	if actor == "" {
		return ""
	}
	return " by " + actor
}

// applyRaw validates raw as the new configuration and applies it. persist,
// when set, runs after everything validated and before anything is applied
// (the settings API writes config.json there); its failure aborts the change.
// The caller holds reload.mu.
func (p *Pool) applyRaw(raw []byte, trigger, actor string, persist func() error) (report api.ReloadReport) {
	report = api.ReloadReport{At: time.Now().UTC(), Trigger: trigger, Actor: actor, Changes: []string{}, Errors: []string{}}
	defer func() { report = p.record(report) }()
	fail := func(err error) api.ReloadReport {
		report.Errors = append(report.Errors, err.Error())
		return report
	}

	next, err := DecodeConfig(raw)
	if err != nil {
		return fail(fmt.Errorf("%s: %w", p.reload.path, err))
	}
	if changed := restartRequired(p.reload.file, next); len(changed) > 0 {
		return fail(fmt.Errorf("these settings need a restart: %v", changed))
	}
	if next.API == nil {
		return fail(errors.New("api configuration is required"))
	}
	if err := validateReloadable(next, p.Engine == nil); err != nil {
		return fail(err)
	}
	ports, err := p.StratumServer.PreparePorts(next.Ports)
	if err != nil {
		return fail(err)
	}
	apiChange, err := p.APIServer.PrepareAPI(next.API)
	if err != nil {
		ports.Abort()
		return fail(err)
	}
	var engineApply func() []string
	if r, ok := p.Engine.(engine.Reconfigurable); ok {
		if engineApply, err = r.PrepareReload(next); err != nil {
			ports.Abort()
			return fail(err)
		}
	}
	if persist != nil {
		if err := persist(); err != nil {
			ports.Abort()
			return fail(err)
		}
	}

	// Everything validated: apply.
	current := p.Live.Get()
	portChanges, portErrs := ports.Commit()
	for _, c := range portChanges {
		report.Changes = append(report.Changes, c.String())
	}
	for _, e := range portErrs {
		report.Errors = append(report.Errors, e.Error())
	}
	report.Changes = append(report.Changes, apiChange.Commit()...)
	if engineApply != nil {
		report.Changes = append(report.Changes, engineApply()...)
	}

	if !reflect.DeepEqual(current.Banning, next.Banning) {
		p.StratumServer.BanningManager.Update(next.Banning)
		report.Changes = append(report.Changes, "banning")
	}
	if current.ConnectionTimeout != next.ConnectionTimeout {
		report.Changes = append(report.Changes, "connectionTimeout")
	}
	if current.TCPProxyProtocol != next.TCPProxyProtocol {
		report.Changes = append(report.Changes, "tcpProxyProtocol (new connections)")
	}
	feesChanged := false
	if !sameRecipients(current.RewardRecipients, next.RewardRecipients) {
		feesChanged = true
		report.Changes = append(report.Changes, "rewardRecipients (from the next job)")
	}

	applied := *current
	applied.Ports = next.Ports
	applied.Banning = next.Banning
	applied.ConnectionTimeout = next.ConnectionTimeout
	applied.TCPProxyProtocol = next.TCPProxyProtocol
	applied.API = next.API
	applied.Quantus = next.Quantus
	if feesChanged {
		applied.RewardRecipients = next.RewardRecipients
	}
	if current.PaymentOptions != nil && next.PaymentOptions != nil {
		pay := *current.PaymentOptions
		if pay.Interval != next.PaymentOptions.Interval || pay.MinPayment != next.PaymentOptions.MinPayment || pay.MinConfirmations != next.PaymentOptions.MinConfirmations {
			pay.Interval = next.PaymentOptions.Interval
			pay.MinPayment = next.PaymentOptions.MinPayment
			pay.MinConfirmations = next.PaymentOptions.MinConfirmations
			if p.PaymentManager != nil {
				p.PaymentManager.Update(&pay)
			}
			report.Changes = append(report.Changes, "payment interval/minPayment/minConfirmations (from the next payout run)")
		}
		applied.PaymentOptions = &pay
	}
	p.Live.Set(&applied)
	p.APIServer.UpdatePorts(applied.Ports)
	p.Stats.StratumPorts = p.StratumServer.Ports()
	p.reload.file = next
	p.reload.applied = fileVersion(raw)

	if len(report.Changes) == 0 {
		report.Changes = append(report.Changes, "no changes")
	}
	report.OK = len(report.Errors) == 0
	return report
}

// sameRecipients treats a missing list and an empty one as equal.
func sameRecipients(a, b []*config.Recipient) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return bytes.Equal(mustJSON(a), mustJSON(b))
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}
