package stratum

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/storage"
	"github.com/mining-pool/not-only-mining-pool/types"
)

// fakeConn satisfies net.Conn with a TCP local address so port lookups work.
type fakeConn struct{ bytes.Buffer }

func (f *fakeConn) Read(b []byte) (int, error)  { return 0, nil }
func (f *fakeConn) Write(b []byte) (int, error) { return len(b), nil }
func (f *fakeConn) Close() error                { return nil }
func (f *fakeConn) LocalAddr() net.Addr         { return &net.TCPAddr{IP: net.IPv4zero, Port: 3032} }
func (f *fakeConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000}
}
func (f *fakeConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

// fakeEngine implements engine.Engine plus the per-difficulty job capability.
type fakeEngine struct {
	submits   [][]interface{}
	valid     bool
	shareDiff float64 // if >0, the achieved difficulty OnSubmit reports
}

func (f *fakeEngine) Name() string                     { return "fake" }
func (f *fakeEngine) Init(_ *config.Options) error     { return nil }
func (f *fakeEngine) Watch(_ func()) error            { select {} }
func (f *fakeEngine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	return true, nil, 0
}
func (f *fakeEngine) JobNotification(_ bool) (string, []interface{}) {
	return "", []interface{}{"0xheader", "0xseed", "0xtarget-global"}
}
func (f *fakeEngine) JobParamsForDifficulty(diff float64) []interface{} {
	return []interface{}{"0xheader", "0xseed", diff}
}
func (f *fakeEngine) OnSubmit(s engine.Session, params []interface{}) *types.Share {
	f.submits = append(f.submits, params)
	diff := s.Difficulty()
	if f.shareDiff > 0 {
		diff = f.shareDiff
	}
	share := &types.Share{Miner: s.WorkerName(), RemoteAddr: s.RemoteAddr(), Diff: diff}
	if !f.valid {
		share.ErrorCode = types.ErrLowDiffShare
	}
	return share
}

func newEngineTestClient(eng engine.Engine) (*Client, *bytes.Buffer) {
	out := new(bytes.Buffer)
	conn := &fakeConn{}
	return &Client{
		Options: &config.Options{
			Ports:   map[int]*config.PortOptions{3032: {Diff: 8}},
			Banning: &config.BanningOptions{CheckThreshold: 1000, InvalidPercent: 50},
		},
		Socket:            conn,
		SocketBufIO:       bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(out)),
		RemoteAddress:     conn.RemoteAddr(),
		Shares:            &Shares{},
		SocketClosedEvent: make(chan struct{}, 1),
		Engine:            eng,
	}, out
}

func req(method string, params ...interface{}) *daemons.JsonRpcRequest {
	return &daemons.JsonRpcRequest{Id: int64(1), Method: method, Params: daemons.MarshalParams(params...)}
}

func drainResponses(t *testing.T, out *bytes.Buffer) []map[string]interface{} {
	t.Helper()
	var msgs []map[string]interface{}
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("bad json line %q: %v", line, err)
		}
		msgs = append(msgs, m)
	}
	out.Reset()
	return msgs
}

func TestEngineLoginPushesWorkAtPortDiff(t *testing.T) {
	eng := &fakeEngine{valid: true}
	sc, out := newEngineTestClient(eng)

	sc.HandleMessage(req("eth_submitLogin", "0xworker", "x"))

	msgs := drainResponses(t, out)
	if len(msgs) != 2 {
		t.Fatalf("want login reply + work push, got %d msgs: %v", len(msgs), msgs)
	}
	if msgs[0]["result"] != true {
		t.Fatalf("login reply should be true: %v", msgs[0])
	}
	work, _ := msgs[1]["result"].([]interface{})
	if len(work) != 3 || work[0] != "0xheader" {
		t.Fatalf("work push wrong: %v", msgs[1])
	}
	if diff, _ := work[2].(float64); diff != 8 {
		t.Fatalf("work should carry the port default diff 8, got %v", work[2])
	}
	if !sc.IsAuthorized || sc.WorkerName != "0xworker" {
		t.Fatalf("client not authorized after login: %v %q", sc.IsAuthorized, sc.WorkerName)
	}
}

func TestEngineGetWorkAndSubmitRouting(t *testing.T) {
	eng := &fakeEngine{valid: true}
	sc, out := newEngineTestClient(eng)
	sc.HandleMessage(req("eth_submitLogin", "0xworker"))
	drainResponses(t, out)

	sc.HandleMessage(req("eth_getWork"))
	msgs := drainResponses(t, out)
	if len(msgs) != 1 {
		t.Fatalf("want one getWork reply, got %v", msgs)
	}

	sc.HandleMessage(req("eth_submitWork", "0xnonce", "0xheader", "0xmix"))
	msgs = drainResponses(t, out)
	if len(msgs) != 1 || msgs[0]["result"] != true {
		t.Fatalf("valid submit should reply true: %v", msgs)
	}
	if len(eng.submits) != 1 || eng.submits[0][0] != "0xnonce" {
		t.Fatalf("engine did not receive submit params: %v", eng.submits)
	}
}

// notifyEngine mimics a kawpow-style engine: work is pushed via mining.notify
// and subscribe assigns an extranonce prefix.
type notifyEngine struct{ fakeEngine }

func (n *notifyEngine) NotifyMethod() string { return "mining.notify" }
func (n *notifyEngine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	return []interface{}{nil, "ab01"}, []byte{0xab, 0x01}, 6
}

func TestEngineNotifyDialect(t *testing.T) {
	eng := &notifyEngine{fakeEngine{valid: true}}
	sc, out := newEngineTestClient(eng)

	// subscribe: reply carries the extranonce, no work push yet
	sc.HandleMessage(req("mining.subscribe", "kawpowminer/1.7"))
	msgs := drainResponses(t, out)
	if len(msgs) != 1 {
		t.Fatalf("subscribe should only reply, got %v", msgs)
	}
	res, _ := msgs[0]["result"].([]interface{})
	if len(res) != 2 || res[1] != "ab01" {
		t.Fatalf("subscribe result should be [null, extranonce]: %v", msgs[0])
	}
	if sc.ExtraNonce1 == nil || sc.ExtraNonce1[0] != 0xab {
		t.Fatal("extranonce not stored on the client")
	}

	// authorize: true reply + a mining.notify push
	sc.HandleMessage(req("mining.authorize", "RWorkerAddr", "x"))
	msgs = drainResponses(t, out)
	if len(msgs) != 2 {
		t.Fatalf("authorize should reply + notify, got %v", msgs)
	}
	if msgs[0]["result"] != true {
		t.Fatalf("authorize should reply true: %v", msgs[0])
	}
	if msgs[1]["method"] != "mining.notify" {
		t.Fatalf("work must be pushed as mining.notify: %v", msgs[1])
	}
}

// targetEngine mimics a zcash-style engine: a mining.set_target push precedes
// every mining.notify.
type targetEngine struct{ notifyEngine }

func (z *targetEngine) TargetParams(diff float64) (string, []interface{}) {
	return "mining.set_target", []interface{}{diff}
}

func TestEngineSetTargetDialect(t *testing.T) {
	eng := &targetEngine{notifyEngine{fakeEngine{valid: true}}}
	sc, out := newEngineTestClient(eng)

	sc.HandleMessage(req("mining.subscribe", "gminer/3.4"))
	drainResponses(t, out)

	sc.HandleMessage(req("mining.authorize", "t1Worker", "x"))
	msgs := drainResponses(t, out)
	if len(msgs) != 3 {
		t.Fatalf("authorize should reply + set_target + notify, got %d: %v", len(msgs), msgs)
	}
	if msgs[1]["method"] != "mining.set_target" {
		t.Fatalf("set_target must precede notify: %v", msgs[1])
	}
	if p, _ := msgs[1]["params"].([]interface{}); len(p) != 1 || p[0] != float64(8) {
		t.Fatalf("set_target must carry the port default diff: %v", msgs[1])
	}
	if msgs[2]["method"] != "mining.notify" {
		t.Fatalf("notify must follow set_target: %v", msgs[2])
	}
}

// cnEngine mimics the CryptoNote/XMRig dialect: object params, login reply
// embeds the job, pushes use method "job" with a bare object.
type cnEngine struct{ fakeEngine }

func (c *cnEngine) NotifyMethod() string { return "job" }
func (c *cnEngine) ObjectParams() bool   { return true }
func (c *cnEngine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	return map[string]interface{}{"id": "1", "job": map[string]interface{}{"job_id": "j1"}, "status": "OK"}, nil, 0
}
func (c *cnEngine) JobParamsForDifficulty(_ float64) []interface{} {
	return []interface{}{map[string]interface{}{"job_id": "j1", "blob": "aa"}}
}

func TestEngineCryptoNoteDialect(t *testing.T) {
	eng := &cnEngine{fakeEngine{valid: true}}
	sc, out := newEngineTestClient(eng)

	// login with OBJECT params: reply embeds the job, no push follows
	sc.HandleMessage(&daemons.JsonRpcRequest{
		Id: int64(1), Method: "login",
		Params: json.RawMessage(`{"login":"4xxWALLET","pass":"x","agent":"xmrig/6.21"}`),
	})
	msgs := drainResponses(t, out)
	if len(msgs) != 1 {
		t.Fatalf("login should reply once (job embedded), got %v", msgs)
	}
	res, _ := msgs[0]["result"].(map[string]interface{})
	if res == nil || res["status"] != "OK" || res["job"] == nil {
		t.Fatalf("login result must embed job+status: %v", msgs[0])
	}
	if sc.WorkerName != "4xxWALLET" || !sc.IsAuthorized {
		t.Fatalf("login must authorize with the wallet as worker: %q", sc.WorkerName)
	}

	// getjob returns the bare job object
	sc.HandleMessage(req("getjob"))
	msgs = drainResponses(t, out)
	if r, _ := msgs[0]["result"].(map[string]interface{}); r == nil || r["job_id"] != "j1" {
		t.Fatalf("getjob must return the job object: %v", msgs[0])
	}

	// submit with object params: engine sees it, reply is {"status":"OK"}
	sc.HandleMessage(&daemons.JsonRpcRequest{
		Id: int64(2), Method: "submit",
		Params: json.RawMessage(`{"id":"1","job_id":"j1","nonce":"01020304","result":"aa"}`),
	})
	msgs = drainResponses(t, out)
	if r, _ := msgs[0]["result"].(map[string]interface{}); r == nil || r["status"] != "OK" {
		t.Fatalf("submit reply must be {status:OK}: %v", msgs[0])
	}
	if len(eng.submits) != 1 {
		t.Fatal("engine did not receive the submit")
	}
	if obj, ok := eng.submits[0][0].(map[string]interface{}); !ok || obj["job_id"] != "j1" {
		t.Fatalf("engine must receive the object params: %v", eng.submits[0])
	}

	// keepalived
	sc.HandleMessage(req("keepalived"))
	msgs = drainResponses(t, out)
	if r, _ := msgs[0]["result"].(map[string]interface{}); r == nil || r["status"] != "KEEPALIVED" {
		t.Fatalf("keepalived reply wrong: %v", msgs[0])
	}
}

func TestEngineRejectsInvalidShareAndUnauthorized(t *testing.T) {
	eng := &fakeEngine{valid: false}
	sc, out := newEngineTestClient(eng)

	// unauthorized submit is refused before reaching the engine
	sc.HandleMessage(req("eth_submitWork", "0xn", "0xh", "0xm"))
	msgs := drainResponses(t, out)
	if len(msgs) != 1 || msgs[0]["error"] == nil {
		t.Fatalf("unauthorized submit should error: %v", msgs)
	}
	if len(eng.submits) != 0 {
		t.Fatal("engine must not see unauthorized submits")
	}

	sc.HandleMessage(req("eth_submitLogin", "0xworker"))
	drainResponses(t, out)

	sc.HandleMessage(req("eth_submitWork", "0xn", "0xh", "0xm"))
	msgs = drainResponses(t, out)
	if len(msgs) != 1 || msgs[0]["result"] != false || msgs[0]["error"] == nil {
		t.Fatalf("invalid share should reply false with error: %v", msgs)
	}
}

// A valid engine share is persisted for stats/accounting, credited at the
// assigned difficulty and keyed by the miner's payout address (worker.rig split).
func TestEngineSharePersisted(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	host, portStr, _ := net.SplitHostPort(mr.Addr())
	port, _ := strconv.Atoi(portStr)
	db := storage.NewStorage("TESTENG", &config.RedisOptions{Network: "tcp", Host: host, Port: port})

	eng := &fakeEngine{valid: true}
	sc, _ := newEngineTestClient(eng)
	sc.DB = db
	sc.IsAuthorized = true
	sc.WorkerName = "minerAddr.rig1"
	sc.CurrentDifficulty = big.NewFloat(8)

	sc.HandleMessage(req("eth_submitWork", "0xn", "0xh", "0xm"))

	// PutShare runs asynchronously; poll briefly for the round contribution.
	var got string
	for i := 0; i < 100; i++ {
		if got = mr.HGet("TESTENG:shares:roundCurrent", "minerAddr"); got != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got != "8" {
		t.Errorf("round contribution for minerAddr = %q, want 8 (assigned diff)", got)
	}
	if ok, _ := mr.SIsMember("TESTENG:miner:minerAddr:rigs", "rig1"); !ok {
		t.Error("rig1 should be indexed under minerAddr")
	}
	if ok, _ := mr.SIsMember("TESTENG:pool:miners", "minerAddr"); !ok {
		t.Error("minerAddr should be indexed in pool:miners")
	}
}

// After a vardiff retarget raises the difficulty, a share that still meets the
// previous difficulty is honoured; one below both is rejected.
func TestEngineVarDiffBoundaryTolerance(t *testing.T) {
	eng := &fakeEngine{valid: false, shareDiff: 8} // engine would flag ErrLowDiffShare
	sc, out := newEngineTestClient(eng)
	sc.IsAuthorized = true
	sc.PreviousDifficulty = big.NewFloat(8) // difficulty before the retarget
	sc.CurrentDifficulty = big.NewFloat(16) // retarget raised it

	// achieved 8 meets the previous difficulty -> accepted despite ErrLowDiffShare
	sc.HandleMessage(req("eth_submitWork", "0xn", "0xh", "0xm"))
	msgs := drainResponses(t, out)
	if len(msgs) != 1 || msgs[0]["result"] != true || msgs[0]["error"] != nil {
		t.Fatalf("share meeting the previous difficulty should be accepted: %v", msgs)
	}

	// achieved 4 is below both current and previous -> still rejected
	eng.shareDiff = 4
	sc.HandleMessage(req("eth_submitWork", "0xn", "0xh", "0xm"))
	msgs = drainResponses(t, out)
	if len(msgs) != 1 || msgs[0]["result"] != false || msgs[0]["error"] == nil {
		t.Fatalf("share below both difficulties should be rejected: %v", msgs)
	}
}
