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
		w.mu.Unlock()
		result = map[string]interface{}{"ismine": true, "isvalid": true, "address": req.Params[0]}
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

// seedRound writes a sealed round + its pending block, as PutShare would.
func (h *harness) seedRound(height uint64, txHash string, shares map[string]float64) {
	for miner, diff := range shares {
		h.mr.HSet("TEST:shares:round"+strconv.FormatUint(height, 10), miner, strconv.FormatFloat(diff, 'f', -1, 64))
	}
	pb := &storage.PendingBlock{Hash: "blk" + strconv.FormatUint(height, 10), TxHash: txHash, Height: height}
	h.mr.SAdd("TEST:blocks:pending", pb.String())
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
	if got.Interval != 600 || got.MinConfirmations != 100 || got.AddressCheckMethod != "getaddressinfo" {
		t.Errorf("unexpected defaults: %+v", got)
	}
	if got.SendManyDummy != "" || got.OmitSendManyDummy {
		t.Errorf("sendmany dummy default should be present-empty, got %q omit=%v", got.SendManyDummy, got.OmitSendManyDummy)
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
