package payments

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/storage"
)

const poolAddr = "mPoolPayoutAddressXXXXXXXXXXXXXXXXX"

// fakeWallet is a minimal bitcoind-style JSON-RPC wallet. Tests override the
// gettransaction/sendmany behaviour; getaddressinfo/getbalance have defaults.
type fakeWallet struct {
	mu       sync.Mutex
	balance  string // raw getbalance result
	sendmany func(dummyPresent bool, amounts map[string]float64) (string, *daemons.JsonRpcError)
	gettx    func(txid string) (*daemons.GetTransaction, *daemons.JsonRpcError)

	SentBatches []map[string]float64 // captured sendmany calls
	AddrMethod  string               // captured address-check method actually called
	badAddr     map[string]bool      // addresses the wallet rejects as unpayable
}

func (w *fakeWallet) handler(rw http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Id     interface{}   `json:"id"`
		Method string        `json:"method"`
		Params []interface{} `json:"params"`
	}
	_ = json.Unmarshal(body, &req)

	var result interface{}
	var rpcErr *daemons.JsonRpcError

	switch req.Method {
	case "getaddressinfo", "validateaddress":
		w.mu.Lock()
		w.AddrMethod = req.Method
		bad := w.badAddr[req.Params[0].(string)]
		w.mu.Unlock()
		if bad {
			// getaddressinfo rejects a malformed address with an error.
			rpcErr = &daemons.JsonRpcError{Code: -5, Message: "Invalid address"}
			break
		}
		result = map[string]interface{}{"ismine": true, "isvalid": true, "address": req.Params[0], "labels": []string{""}}
	case "getbalance":
		// getbalance returns a bare number; emit it raw.
		writeRPC(rw, req.Id, json.RawMessage(w.balance), nil)
		return
	case "gettransaction":
		gt, e := w.gettx(req.Params[0].(string))
		result, rpcErr = gt, e
	case "sendmany":
		dummyPresent := false
		var amounts map[string]float64
		if len(req.Params) == 2 { // [dummy, {addr:amt}]
			dummyPresent = true
			amounts = toAmounts(req.Params[1])
		} else if len(req.Params) == 1 { // [{addr:amt}]
			amounts = toAmounts(req.Params[0])
		}
		w.mu.Lock()
		w.SentBatches = append(w.SentBatches, amounts)
		w.mu.Unlock()
		txid, e := w.sendmany(dummyPresent, amounts)
		result, rpcErr = txid, e
	default:
		rpcErr = &daemons.JsonRpcError{Code: -32601, Message: "method not found: " + req.Method}
	}

	raw, _ := json.Marshal(result)
	writeRPC(rw, req.Id, raw, rpcErr)
}

func toAmounts(v interface{}) map[string]float64 {
	out := map[string]float64{}
	if m, ok := v.(map[string]interface{}); ok {
		for k, val := range m {
			if f, ok := val.(float64); ok {
				out[k] = f
			}
		}
	}
	return out
}

func writeRPC(rw http.ResponseWriter, id interface{}, result json.RawMessage, rpcErr *daemons.JsonRpcError) {
	resp := map[string]interface{}{"id": id, "result": result}
	if rpcErr != nil {
		resp["result"] = nil
		resp["error"] = rpcErr
	}
	_ = json.NewEncoder(rw).Encode(resp)
}

// harness wires a PaymentManager to miniredis + the fakeWallet.
type harness struct {
	pm     *PaymentManager
	db     *storage.DB
	wallet *fakeWallet
	mr     *miniredis.Miniredis
}

func newHarness(t *testing.T, opts *config.PaymentOptions) *harness {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)

	host, portStr := splitHostPort(mr.Addr())
	port, _ := strconv.Atoi(portStr)
	db := storage.NewStorage("TEST", &config.RedisOptions{Network: "tcp", Host: host, Port: port})

	w := &fakeWallet{balance: "12.34567890"}
	srv := httptest.NewServer(http.HandlerFunc(w.handler))
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL)
	dport, _ := strconv.Atoi(u.Port())
	dm := daemons.NewDaemonManager([]*config.DaemonOptions{{Host: u.Hostname(), Port: dport}}, &config.CoinOptions{Name: "TEST"})

	pm := NewPaymentManager(opts, &config.Recipient{Address: poolAddr, Type: "p2pkh"}, dm, db)
	return &harness{pm: pm, db: db, wallet: w, mr: mr}
}

func splitHostPort(addr string) (host, port string) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:]
		}
	}
	return addr, ""
}

// seedRoundShares writes a sealed round's per-miner contribution.
func (h *harness) seedRoundShares(height uint64, shares map[string]float64) {
	for miner, diff := range shares {
		h.mr.HSet("TEST:shares:round"+strconv.FormatUint(height, 10), miner, strconv.FormatFloat(diff, 'f', -1, 64))
	}
}

// seedPending records a pending block (with optional finder / pplns mark).
func (h *harness) seedPending(height uint64, txHash, finder string, mark int64) {
	pb := &storage.PendingBlock{Hash: "blk" + strconv.FormatUint(height, 10), TxHash: txHash, Height: height, Finder: finder, Mark: mark}
	h.mr.SAdd("TEST:blocks:pending", pb.String())
}

// seedRound seals a round + its pending block, as PutShare would (prop mode).
func (h *harness) seedRound(height uint64, txHash string, shares map[string]float64) {
	h.seedRoundShares(height, shares)
	h.seedPending(height, txHash, "", 0)
}

// seedPPLNS appends one share to the pplns log at a monotonic sequence.
func (h *harness) seedPPLNS(miner string, diff float64, seq int64) {
	h.mr.ZAdd("TEST:shares:pplnslog", float64(seq),
		miner+":"+strconv.FormatFloat(diff, 'f', -1, 64)+":"+strconv.FormatInt(seq, 10))
}

func generateTx(confirmations int, reward float64) func(string) (*daemons.GetTransaction, *daemons.JsonRpcError) {
	return func(txid string) (*daemons.GetTransaction, *daemons.JsonRpcError) {
		return &daemons.GetTransaction{
			Amount:        reward,
			Confirmations: confirmations,
			Generated:     true,
			Details:       []daemons.GetTransactionDetail{{Address: poolAddr, Category: "generate", Amount: reward}},
		}, nil
	}
}

func TestPayout_FullFlow(t *testing.T) {
	h := newHarness(t, &config.PaymentOptions{Interval: 1, MinPayment: 0, MinConfirmations: 100})
	if err := h.pm.Init(); err != nil {
		t.Fatal(err)
	}
	// round 100: A contributed 30% of shares, B 70%; block reward 50 coin.
	h.seedRound(100, "tx100", map[string]float64{"minerA": 30, "minerB": 70})
	h.wallet.gettx = generateTx(120, 50.0) // mature
	h.wallet.sendmany = func(dummy bool, amounts map[string]float64) (string, *daemons.JsonRpcError) {
		if !dummy {
			t.Errorf("expected the default sendmany dummy arg to be present")
		}
		return "payouttxid", nil
	}

	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}

	// exactly one sendmany, split 15 / 35.
	if len(h.wallet.SentBatches) != 1 {
		t.Fatalf("want 1 sendmany, got %d", len(h.wallet.SentBatches))
	}
	sent := h.wallet.SentBatches[0]
	if got := sent["minerA"]; got != 15.0 {
		t.Errorf("minerA payout = %v, want 15", got)
	}
	if got := sent["minerB"]; got != 35.0 {
		t.Errorf("minerB payout = %v, want 35", got)
	}

	// payouts recorded, balances zeroed (fully paid), block confirmed, round gone.
	if v := h.mr.HGet("TEST:payouts", "minerA"); v != "15" {
		t.Errorf("payouts minerA = %q, want 15", v)
	}
	if v := h.mr.HGet("TEST:balances", "minerB"); v != "0" {
		t.Errorf("balance minerB = %q, want 0", v)
	}
	if ok, _ := h.mr.SIsMember("TEST:blocks:confirmed", (&storage.PendingBlock{Hash: "blk100", TxHash: "tx100", Height: 100}).String()); !ok {
		t.Error("block 100 was not moved to confirmed")
	}
	if h.mr.Exists("TEST:shares:round100") {
		t.Error("sealed round 100 should be deleted after payout")
	}
}

func TestPayout_ImmatureStaysPending(t *testing.T) {
	h := newHarness(t, &config.PaymentOptions{MinPayment: 0, MinConfirmations: 100})
	_ = h.pm.Init()
	h.seedRound(101, "tx101", map[string]float64{"minerA": 100})
	h.wallet.gettx = generateTx(10, 50.0) // only 10 confs < 100
	h.wallet.sendmany = func(bool, map[string]float64) (string, *daemons.JsonRpcError) {
		t.Fatal("must not pay an immature block")
		return "", nil
	}
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := h.mr.SIsMember("TEST:blocks:pending", (&storage.PendingBlock{Hash: "blk101", TxHash: "tx101", Height: 101}).String()); !ok {
		t.Error("immature block must remain pending")
	}
}

func TestPayout_OrphanNotPaid(t *testing.T) {
	h := newHarness(t, &config.PaymentOptions{MinPayment: 0, MinConfirmations: 100})
	_ = h.pm.Init()
	h.seedRound(102, "tx102", map[string]float64{"minerA": 100})
	h.wallet.gettx = func(string) (*daemons.GetTransaction, *daemons.JsonRpcError) {
		return nil, &daemons.JsonRpcError{Code: -5, Message: "Invalid or non-wallet transaction id"}
	}
	h.wallet.sendmany = func(bool, map[string]float64) (string, *daemons.JsonRpcError) {
		t.Fatal("must not pay an orphaned block")
		return "", nil
	}
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := h.mr.SIsMember("TEST:blocks:orphaned", (&storage.PendingBlock{Hash: "blk102", TxHash: "tx102", Height: 102}).String()); !ok {
		t.Error("orphaned block must move to orphaned set")
	}
	if h.mr.Exists("TEST:shares:round102") {
		t.Error("orphaned round shares should be cleared")
	}
}

func TestPayout_BelowMinPaymentCarriesOver(t *testing.T) {
	// min payment 10 coin; A earns 15 (paid), B earns 5 (carried over as balance).
	h := newHarness(t, &config.PaymentOptions{MinPayment: 10, MinConfirmations: 100})
	_ = h.pm.Init()
	h.seedRound(103, "tx103", map[string]float64{"minerA": 75, "minerB": 25})
	h.wallet.gettx = generateTx(120, 20.0) // reward 20 -> A=15, B=5
	h.wallet.sendmany = func(_ bool, amounts map[string]float64) (string, *daemons.JsonRpcError) {
		if _, ok := amounts["minerB"]; ok {
			t.Errorf("minerB below min payment must not be sent")
		}
		return "txid", nil
	}
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
	if v := h.mr.HGet("TEST:balances", "minerB"); v != "5" {
		t.Errorf("minerB carried balance = %q, want 5", v)
	}
	if v := h.mr.HGet("TEST:payouts", "minerA"); v != "15" {
		t.Errorf("minerA payout = %q, want 15", v)
	}
}

// --- pay modes ---

func TestPayout_Solo(t *testing.T) {
	h := newHarness(t, &config.PaymentOptions{MinPayment: 0, MinConfirmations: 100, PayMode: "solo"})
	if err := h.pm.Init(); err != nil {
		t.Fatal(err)
	}
	// round shares exist for other miners, but solo pays only the block finder.
	h.seedRoundShares(300, map[string]float64{"minerA": 90, "minerB": 10})
	h.seedPending(300, "tx300", "minerFinder", 0)
	h.wallet.gettx = generateTx(120, 50.0)
	h.wallet.sendmany = func(_ bool, amounts map[string]float64) (string, *daemons.JsonRpcError) {
		if amounts["minerFinder"] != 50.0 {
			t.Errorf("solo finder payout = %v, want 50", amounts["minerFinder"])
		}
		if _, ok := amounts["minerA"]; ok {
			t.Errorf("solo must not pay non-finders")
		}
		return "txid", nil
	}
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
	if v := h.mr.HGet("TEST:payouts", "minerFinder"); v != "50" {
		t.Errorf("finder payout = %q, want 50", v)
	}
}

func TestPayout_PPLNS(t *testing.T) {
	h := newHarness(t, &config.PaymentOptions{MinPayment: 0, MinConfirmations: 100, PayMode: "pplns", PPLNSWindow: 40})
	if err := h.pm.Init(); err != nil {
		t.Fatal(err)
	}
	// pplns log (seq → miner:diff): A older, then B, then C. Window=40 looks back
	// from the block mark (seq 6): seq 6,5 (C,C) + 4,3 (B,B) = 40 → {B:20,C:20};
	// minerA's older shares fall outside the window and get nothing.
	for _, e := range []struct {
		miner string
		seq   int64
	}{{"minerA", 1}, {"minerA", 2}, {"minerB", 3}, {"minerB", 4}, {"minerC", 5}, {"minerC", 6}} {
		h.seedPPLNS(e.miner, 10, e.seq)
	}
	h.seedPending(301, "tx301", "minerC", 6) // block found at seq 6
	h.wallet.gettx = generateTx(120, 40.0)   // reward 40 -> B:20, C:20
	h.wallet.sendmany = func(_ bool, amounts map[string]float64) (string, *daemons.JsonRpcError) {
		if amounts["minerB"] != 20.0 || amounts["minerC"] != 20.0 {
			t.Errorf("pplns window split wrong: %v", amounts)
		}
		if _, ok := amounts["minerA"]; ok {
			t.Errorf("minerA is outside the pplns window and must not be paid")
		}
		return "txid", nil
	}
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
}

func TestPayout_PPS(t *testing.T) {
	// ppsRate 0.5 coin per diff unit: A(10 diff)->5, B(20 diff)->10. The found
	// block's 50-coin reward funds the wallet and is NOT distributed.
	h := newHarness(t, &config.PaymentOptions{MinPayment: 0, MinConfirmations: 100, PayMode: "pps", PPSRate: 0.5})
	if err := h.pm.Init(); err != nil {
		t.Fatal(err)
	}
	h.seedPPLNS("minerA", 10, 1)
	h.seedPPLNS("minerB", 20, 2)
	h.seedPending(400, "tx400", "minerB", 2)
	h.wallet.gettx = generateTx(120, 50.0) // mature -> block gets confirmed
	h.wallet.sendmany = func(_ bool, amounts map[string]float64) (string, *daemons.JsonRpcError) {
		if amounts["minerA"] != 5.0 || amounts["minerB"] != 10.0 {
			t.Errorf("pps payout wrong (want A=5 B=10): %v", amounts)
		}
		return "txid", nil
	}
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
	if v := h.mr.HGet("TEST:payouts", "minerB"); v != "10" {
		t.Errorf("pps payout minerB = %q, want 10", v)
	}
	// block confirmed (reward funds the wallet, not split) and cursor advanced.
	if ok, _ := h.mr.SIsMember("TEST:blocks:confirmed", (&storage.PendingBlock{Hash: "blk400", TxHash: "tx400", Height: 400, Finder: "minerB", Mark: 2}).String()); !ok {
		t.Error("matured block should be confirmed in pps mode")
	}
	if got, _ := h.mr.Get("TEST:pps:cursor"); got != "2" {
		t.Errorf("pps cursor = %q, want 2", got)
	}
	// re-run: no new shares -> no further payout.
	h.wallet.SentBatches = nil
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
	for _, b := range h.wallet.SentBatches {
		if len(b) > 0 {
			t.Errorf("pps re-run must not pay already-credited shares: %v", b)
		}
	}
}

func TestPPS_RequiresRate(t *testing.T) {
	h := newHarness(t, &config.PaymentOptions{PayMode: "pps"}) // ppsRate defaults to 0
	if err := h.pm.Init(); err == nil {
		t.Error("payMode pps without ppsRate must fail Init")
	}
}

// --- fork configurability ---

func TestForkConfig_ValidateAddressAndNoDummy(t *testing.T) {
	h := newHarness(t, &config.PaymentOptions{
		MinPayment:         0,
		MinConfirmations:   40, // e.g. a fork with 40-block maturity
		AddressCheckMethod: "validateaddress",
		OmitSendManyDummy:  true, // fork whose sendmany drops the leading arg
	})
	if err := h.pm.Init(); err != nil {
		t.Fatal(err)
	}
	if h.wallet.AddrMethod != "validateaddress" {
		t.Errorf("address check used %q, want validateaddress", h.wallet.AddrMethod)
	}
	h.seedRound(200, "tx200", map[string]float64{"minerA": 100})
	h.wallet.gettx = generateTx(50, 10.0) // 50 >= 40 maturity -> mature
	h.wallet.sendmany = func(dummy bool, _ map[string]float64) (string, *daemons.JsonRpcError) {
		if dummy {
			t.Errorf("SendManyDummy=nil should omit the leading arg")
		}
		return "txid", nil
	}
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
	if len(h.wallet.SentBatches) != 1 {
		t.Fatalf("expected a payout, got %d sendmany calls", len(h.wallet.SentBatches))
	}
}

func TestPaymentOptionsDefaults(t *testing.T) {
	got := (&config.PaymentOptions{}).WithDefaults()
	if got.Interval != 600 || got.MinConfirmations != 100 || got.AddressCheckMethod != "getaddressinfo" || got.PayMode != "prop" {
		t.Errorf("unexpected defaults: %+v", got)
	}
	if got.SendManyDummy != "" || got.OmitSendManyDummy {
		t.Errorf("sendmany dummy default should be present-empty, got %q omit=%v", got.SendManyDummy, got.OmitSendManyDummy)
	}
}

// A zero/negative pplnsWindow is documented to fall back to the block's round,
// not pay every retained share across earlier rounds.
func TestPayout_PPLNSZeroWindowFallsBackToRound(t *testing.T) {
	h := newHarness(t, &config.PaymentOptions{MinPayment: 0, MinConfirmations: 100, PayMode: "pplns", PPLNSWindow: 0})
	if err := h.pm.Init(); err != nil {
		t.Fatal(err)
	}
	// round 500 contains only minerA; the pplns log additionally holds minerB from
	// an earlier round. With window 0 we must pay the round (A only), not the log.
	h.seedRoundShares(500, map[string]float64{"minerA": 100})
	h.seedPPLNS("minerA", 10, 1)
	h.seedPPLNS("minerB", 10, 2)
	h.seedPending(500, "tx500", "minerA", 2) // mark past both log entries
	h.wallet.gettx = generateTx(120, 50.0)
	h.wallet.sendmany = func(_ bool, amounts map[string]float64) (string, *daemons.JsonRpcError) {
		if amounts["minerA"] != 50.0 {
			t.Errorf("minerA payout = %v, want the full 50 from its round", amounts["minerA"])
		}
		if _, ok := amounts["minerB"]; ok {
			t.Errorf("minerB is from an earlier round and must not be paid: %v", amounts)
		}
		return "txid", nil
	}
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
}

// One unpayable worker name must not fail the whole batch: it is skipped and its
// balance carries over while everyone else is still paid.
func TestPayout_InvalidAddressSkipped(t *testing.T) {
	h := newHarness(t, &config.PaymentOptions{MinPayment: 0, MinConfirmations: 100})
	h.wallet.badAddr = map[string]bool{"minerBad": true}
	if err := h.pm.Init(); err != nil {
		t.Fatal(err)
	}
	h.seedRound(600, "tx600", map[string]float64{"minerGood": 30, "minerBad": 70})
	h.wallet.gettx = generateTx(120, 100.0) // good->30, bad->70
	h.wallet.sendmany = func(_ bool, amounts map[string]float64) (string, *daemons.JsonRpcError) {
		if amounts["minerGood"] != 30.0 {
			t.Errorf("minerGood payout = %v, want 30", amounts["minerGood"])
		}
		if _, ok := amounts["minerBad"]; ok {
			t.Errorf("unpayable minerBad must be excluded from sendmany: %v", amounts)
		}
		return "txid", nil
	}
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
	if v := h.mr.HGet("TEST:balances", "minerBad"); v != "70" {
		t.Errorf("unpayable balance should carry over, got %q want 70", v)
	}
	if ok, _ := h.mr.SIsMember("TEST:blocks:confirmed", (&storage.PendingBlock{Hash: "blk600", TxHash: "tx600", Height: 600}).String()); !ok {
		t.Error("block should still confirm when a payable miner was paid")
	}
}

// Wallet RPCs must reach the configured payment.daemon index, not always daemon 0.
func TestPayout_UsesConfiguredDaemonIndex(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	host, portStr := splitHostPort(mr.Addr())
	port, _ := strconv.Atoi(portStr)
	db := storage.NewStorage("TEST", &config.RedisOptions{Network: "tcp", Host: host, Port: port})

	// daemon 0 is a mining node that has no wallet; every wallet RPC errors there.
	mining := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var req struct{ Id interface{} }
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		writeRPC(rw, req.Id, nil, &daemons.JsonRpcError{Code: -32601, Message: "no wallet on mining node"})
	}))
	t.Cleanup(mining.Close)
	w := &fakeWallet{balance: "12.34567890"}
	wallet := httptest.NewServer(http.HandlerFunc(w.handler))
	t.Cleanup(wallet.Close)

	toDaemon := func(s *httptest.Server) *config.DaemonOptions {
		u, _ := url.Parse(s.URL)
		p, _ := strconv.Atoi(u.Port())
		return &config.DaemonOptions{Host: u.Hostname(), Port: p}
	}
	dm := daemons.NewDaemonManager([]*config.DaemonOptions{toDaemon(mining), toDaemon(wallet)}, &config.CoinOptions{Name: "TEST"})
	pm := NewPaymentManager(&config.PaymentOptions{MinPayment: 0, MinConfirmations: 100, Daemon: 1}, &config.Recipient{Address: poolAddr, Type: "p2pkh"}, dm, db)
	h := &harness{pm: pm, db: db, wallet: w, mr: mr}

	if err := h.pm.Init(); err != nil {
		t.Fatalf("Init must reach the wallet at daemon index 1: %v", err)
	}
	h.seedRound(700, "tx700", map[string]float64{"minerA": 100})
	h.wallet.gettx = generateTx(120, 50.0)
	h.wallet.sendmany = func(_ bool, _ map[string]float64) (string, *daemons.JsonRpcError) { return "txid", nil }
	if err := h.pm.processPayments(); err != nil {
		t.Fatal(err)
	}
	if len(h.wallet.SentBatches) != 1 {
		t.Fatalf("payout should route to the wallet daemon, got %d sendmany calls", len(h.wallet.SentBatches))
	}
}

func TestCoinSatConversion(t *testing.T) {
	pm := &PaymentManager{Magnitude: 1e8}
	if pm.CoinToSat(1.23456789) != 123456789 {
		t.Errorf("CoinToSat = %d", pm.CoinToSat(1.23456789))
	}
	if pm.SatToCoin(123456789) != 1.23456789 {
		t.Errorf("SatToCoin = %v", pm.SatToCoin(123456789))
	}
	if pm.CoinToSat(0) != 0 || pm.CoinToSat(-5) != 0 {
		t.Error("non-positive coin must convert to 0 sat")
	}
}
