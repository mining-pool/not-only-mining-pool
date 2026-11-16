package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/config"
)

func TestPoolViewHidesMinerIndexFromPublic(t *testing.T) {
	info := PoolInfo{Miners: []string{"alice", "bob"}}
	public := info.view(false)
	if public.Miners != nil || public.MinerCount != 2 {
		t.Fatalf("public pool view: %+v", public)
	}
	raw, _ := json.Marshal(public)
	if strings.Contains(string(raw), "alice") || strings.Contains(string(raw), `"miners"`) {
		t.Fatalf("miner index leaked: %s", raw)
	}
	if admin := info.view(true); len(admin.Miners) != 2 || admin.MinerCount != 2 {
		t.Fatalf("admin pool view: %+v", admin)
	}
}

// ledger mirrors the shape an engine returns from PaymentStatus.
func ledger() map[string]interface{} {
	out := func(address, amount string) map[string]string {
		return map[string]string{"address": address, "amount": amount}
	}
	return map[string]interface{}{
		"decimals": 12, "finalizedHeight": 900, "unconfirmedCandidates": 1,
		"fault":    "wallet rpc http://10.0.0.5:9944 refused",
		"balances": map[string]string{"alice": "10", "bob": "20"},
		"pending":  map[string]interface{}{"id": "p1", "hash": "0xp", "outputs": []interface{}{out("alice", "1"), out("bob", "2")}},
		"paid": []interface{}{
			map[string]interface{}{"hash": "0x1", "height": 800, "outputs": []interface{}{out("alice", "3"), out("bob", "4")}},
			map[string]interface{}{"hash": "0x2", "height": 850, "outputs": []interface{}{out("bob", "5")}},
		},
		"blocks": []interface{}{map[string]interface{}{"hash": "0xb", "height": 799, "reward": "50"}},
	}
}

func decodePayments(t *testing.T, body string) paymentStatus {
	t.Helper()
	var s paymentStatus
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return s
}

func TestPublicPaymentsOnlyRevealTheRequestedAddress(t *testing.T) {
	s := testServer(t)
	s.RegisterPaymentStatus(func() (interface{}, error) { return ledger(), nil })

	for _, target := range []string{"/api/payments", "/payments"} {
		w := call(s, "GET", target, "", nil, "")
		body := w.Body.String()
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", target, w.Code, body)
		}
		for _, secret := range []string{"alice", "bob", "10.0.0.5"} {
			if strings.Contains(body, secret) {
				t.Fatalf("%s leaked %q: %s", target, secret, body)
			}
		}
		p := decodePayments(t, body)
		if p.FinalizedHeight != 900 || len(p.Blocks) != 1 || p.Pending == nil || p.Pending.Hash != "0xp" || p.Fault == "" {
			t.Fatalf("pool-level payment data missing: %+v", p)
		}
	}

	w := call(s, "GET", "/api/payments?address=alice", "", nil, "")
	if strings.Contains(w.Body.String(), "bob") {
		t.Fatalf("other miner leaked: %s", w.Body.String())
	}
	p := decodePayments(t, w.Body.String())
	if p.Balances["alice"] != "10" || len(p.Balances) != 1 {
		t.Fatalf("balance: %+v", p.Balances)
	}
	if p.Pending == nil || len(p.Pending.Outputs) != 1 || p.Pending.Outputs[0].Amount != "1" {
		t.Fatalf("pending: %+v", p.Pending)
	}
	if len(p.Paid) != 1 || p.Paid[0].Hash != "0x1" || len(p.Paid[0].Outputs) != 1 || p.Paid[0].Outputs[0].Amount != "3" {
		t.Fatalf("paid: %+v", p.Paid)
	}

	// An address with no records gets empty collections, not someone else's.
	p = decodePayments(t, call(s, "GET", "/api/payments?address=carol", "", nil, "").Body.String())
	if len(p.Balances) != 0 || len(p.Paid) != 0 || p.Pending != nil {
		t.Fatalf("unknown address: %+v", p)
	}
}

func TestAdminPaymentsAndPoolKeepFullData(t *testing.T) {
	s := testServer(t)
	s.RegisterPaymentStatus(func() (interface{}, error) { return ledger(), nil })
	if w := call(s, "GET", "/admin-api/payments", "", nil, ""); w.Code != 401 {
		t.Fatalf("admin ledger without session: %d", w.Code)
	}
	cookie, _ := login(t, s, nil)
	p := decodePayments(t, call(s, "GET", "/admin-api/payments", "", cookie, "").Body.String())
	if len(p.Balances) != 2 || len(p.Paid) != 2 || len(p.Pending.Outputs) != 2 || !strings.Contains(p.Fault, "refused") {
		t.Fatalf("admin view lost data: %+v", p)
	}
}

func TestPortsConfigHidesTLSFiles(t *testing.T) {
	s := testServer(t)
	s.ConvertConf(&config.Options{API: s.apiConf, Ports: map[int]*config.PortOptions{
		3333: {Diff: 8},
		4444: {Protocol: "stratum", Diff: 16, TLS: &config.TLSServerOptions{CertFile: "/srv/tls/cert.pem", KeyFile: "/srv/tls/key.pem"}},
	}})
	body := call(s, "GET", "/api/config/ports", "", nil, "").Body.String()
	if strings.Contains(body, "/srv/tls") || strings.Contains(body, "keyFile") {
		t.Fatalf("TLS file paths leaked: %s", body)
	}
	var ports map[string]publicPort
	if err := json.Unmarshal([]byte(body), &ports); err != nil {
		t.Fatal(err)
	}
	if ports["4444"].TLS != true || ports["4444"].Protocol != "stratum" || ports["3333"].TLS || ports["3333"].Diff != 8 {
		t.Fatalf("ports: %+v", ports)
	}
}
