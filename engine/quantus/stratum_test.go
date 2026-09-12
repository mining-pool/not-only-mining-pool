package quantus

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mining-pool/not-only-mining-pool/bans"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/storage"
	"github.com/mining-pool/not-only-mining-pool/stratum"
	"github.com/mining-pool/not-only-mining-pool/types"
	"github.com/quic-go/quic-go"
)

type prefixSession struct {
	session
	prefix []byte
}

func (s prefixSession) ExtraNonce1() []byte { return s.prefix }
func normalized(t *testing.T, v interface{}) interface{} {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out interface{}
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func fixture(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]interface{}
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestLuckyPoolJobFixtures(t *testing.T) {
	e := New()
	defer e.Close()
	dialect := &stratumEngine{Engine: e, diff: 1e10}
	login := fixture(t, "luckypool_login")["result"].(map[string]interface{})
	expected := login["job"].(map[string]interface{})
	e.cur, _ = parseJob(&request{JobID: expected["job_id"].(string), MiningHash: expected["mining_hash"].(string), Difficulty: "10000000000"})
	e.cur.seq = uint64(expected["seq"].(float64))
	prefix, _ := hex.DecodeString(expected["extranonce"].(string))
	e.nonceCounter.Store(binary.BigEndian.Uint32(prefix) - 1)
	reply, en, _ := dialect.OnSubscribe(session{1e10}, nil)
	got := normalized(t, reply).(map[string]interface{})
	if !reflect.DeepEqual(got["job"], expected) {
		t.Fatalf("login job mismatch\ngot %#v\nwant %#v", got["job"], expected)
	}
	if got["status"] != "OK" || !reflect.DeepEqual(got["extensions"], login["extensions"]) {
		t.Fatal(got)
	}
	notify := fixture(t, "luckypool_notify")["params"].(map[string]interface{})
	expected = notify["job"].(map[string]interface{})
	e.cur, _ = parseJob(&request{JobID: expected["job_id"].(string), MiningHash: expected["mining_hash"].(string), Difficulty: "10000000000"})
	e.cur.seq = uint64(expected["seq"].(float64))
	en, _ = hex.DecodeString(expected["extranonce"].(string))
	params := dialect.JobParamsForSession(prefixSession{session{1e10}, en})
	if !reflect.DeepEqual(normalized(t, params[0]), notify) {
		t.Fatalf("notification mismatch %v", params)
	}
}
func TestLuckyPoolNonceOwnershipAndDuplicates(t *testing.T) {
	e := New()
	defer e.Close()
	e.cur, _ = parseJob(&request{JobID: "j", MiningHash: strings.Repeat("00", 32), Difficulty: maxTarget.String()})
	dialect := &stratumEngine{Engine: e, diff: 1}
	_, en, _ := dialect.OnSubscribe(session{1}, nil)
	_, other, _ := dialect.OnSubscribe(session{1}, nil)
	if reflect.DeepEqual(en, other) {
		t.Fatal("reused extranonce")
	}
	s := prefixSession{session{1}, en}
	nonce := hex.EncodeToString(en) + strings.Repeat("00", 60)
	p := map[string]interface{}{"id": hex.EncodeToString(en), "job_id": "j", "nonce": nonce}
	if got := dialect.OnSubmit(s, []interface{}{p}); got.ErrorCode != 0 {
		t.Fatal(got)
	}
	p["nonce"] = strings.ToUpper(nonce)
	if got := dialect.OnSubmit(s, []interface{}{p}); got.ErrorCode != types.ErrDuplicateShare {
		t.Fatal(got)
	}
	// The common engine sees the same duplicate, regardless of miner transport.
	if got := e.OnSubmit(session{1}, []interface{}{"a", "j", nonce}); got.ErrorCode != types.ErrDuplicateShare {
		t.Fatal(got)
	}
	p["id"] = hex.EncodeToString(other)
	if got := dialect.OnSubmit(s, []interface{}{p}); got.ErrorCode == 0 {
		t.Fatal("wrong session accepted")
	}
	p["id"] = hex.EncodeToString(en)
	p["nonce"] = hex.EncodeToString(other) + strings.Repeat("00", 60)
	if got := dialect.OnSubmit(s, []interface{}{p}); got.ErrorCode == 0 {
		t.Fatal("foreign nonce prefix accepted")
	}
	for _, p := range []interface{}{nil, []interface{}{}, map[string]interface{}{}, map[string]interface{}{"login": ""}} {
		if dialect.ValidateLogin([]interface{}{p}) == nil {
			t.Fatal("invalid login accepted")
		}
	}
	if dialect.ValidateLogin([]interface{}{map[string]interface{}{"login": "account.rig"}}) != nil {
		t.Fatal("valid login rejected")
	}
}

// Pre-bound listeners avoid ephemeral-port reservation races. This exercises
// the shared server's multi-listener dispatch and per-port engine selection.
type boundEngine struct {
	*Engine
	listeners map[int]net.Listener
}

func (e *boundEngine) Listen(port int, _ *config.PortOptions) (net.Listener, error) {
	return e.listeners[port], nil
}
func (e *boundEngine) Watch(func()) error { return nil }
func TestQUICAndStratumPortsTogether(t *testing.T) {
	for _, useTLS := range []bool{false, true} {
		t.Run(fmt.Sprint("tls=", useTLS), func(t *testing.T) {
			_, serverTLS, pin := certificate(t)
			e := New()
			defer e.Close()
			e.cur, _ = parseJob(&request{JobID: "mixed", MiningHash: strings.Repeat("00", 32), Difficulty: maxTarget.String()})
			e.cur.seq = 1
			qp := &config.PortOptions{Protocol: "quic", Diff: 1, TLS: serverTLS}
			sp := &config.PortOptions{Protocol: "stratum", Diff: 1}
			if useTLS {
				sp.TLS = serverTLS
			}
			ql, err := e.Listen(0, qp)
			if err != nil {
				t.Fatal(err)
			}
			defer ql.Close()
			sl, err := e.Listen(0, sp)
			if err != nil {
				t.Fatal(err)
			}
			defer sl.Close()
			qport, sport := ql.Addr().(*net.TCPAddr).Port, sl.Addr().(*net.TCPAddr).Port
			if qport == sport {
				t.Skip("OS assigned identical TCP and UDP ports")
			}
			opts := &config.Options{Ports: map[int]*config.PortOptions{qport: qp, sport: sp}}
			server := stratum.NewStratumServer(opts, nil, bans.NewBanningManager(nil))
			server.Engine = &boundEngine{Engine: e, listeners: map[int]net.Listener{qport: ql, sport: sl}}
			mr := miniredis.RunT(t)
			rp, _ := strconv.Atoi(mr.Port())
			server.DB = storage.NewStorage("MIXED", &config.RedisOptions{Host: mr.Host(), Port: rp})
			defer server.DB.Close()
			if got := server.Init(); len(got) != 2 {
				t.Fatal("not all ports started", got)
			}
			tlsConfig, _ := pinnedTLS(pin)
			qc, err := quic.DialAddr(deadlineContext(t), fmt.Sprintf("127.0.0.1:%d", qport), tlsConfig, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer qc.CloseWithError(0, "")
			qs, err := qc.OpenStreamSync(deadlineContext(t))
			if err != nil {
				t.Fatal(err)
			}
			qs.SetDeadline(time.Now().Add(5 * time.Second))
			ready := message{}
			ready.Ready = &struct {
				Token string `json:"token"`
			}{"native.rig"}
			if err = writeMessage(qs, ready); err != nil {
				t.Fatal(err)
			}
			qjob, err := readMessage(qs)
			if err != nil || qjob.NewJob == nil {
				t.Fatalf("QUIC login %v", err)
			}
			var sc net.Conn
			address := fmt.Sprintf("127.0.0.1:%d", sport)
			if useTLS {
				cfg, _ := pinnedTLS(pin)
				cfg.NextProtos = nil
				sc, err = tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", address, cfg)
			} else {
				sc, err = net.DialTimeout("tcp", address, 5*time.Second)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer sc.Close()
			sc.SetDeadline(time.Now().Add(5 * time.Second))
			reader := bufio.NewReader(sc)
			rpc := func(method string, params interface{}) map[string]interface{} {
				t.Helper()
				b, _ := json.Marshal(map[string]interface{}{"id": 1, "method": method, "params": params})
				if _, err = sc.Write(append(b, '\n')); err != nil {
					t.Fatal(err)
				}
				line, err := reader.ReadBytes('\n')
				if err != nil {
					t.Fatal(err)
				}
				var response map[string]interface{}
				if err = json.Unmarshal(line, &response); err != nil {
					t.Fatal(err)
				}
				return response
			}
			login := rpc("login", map[string]interface{}{"login": "stratum.rig", "pass": "x", "agent": "SRBMiner-MULTI/3.6.4"})
			loginResult, ok := login["result"].(map[string]interface{})
			if !ok {
				t.Fatal(login)
			}
			j := loginResult["job"].(map[string]interface{})
			if j["algo"] != "qpow-poseidon2" {
				t.Fatal(j)
			}
			nonce := j["extranonce"].(string) + strings.Repeat("00", 60)
			response := rpc("submit", map[string]interface{}{"id": loginResult["id"], "job_id": j["job_id"], "nonce": nonce})
			if response["error"] != nil || response["result"].(map[string]interface{})["status"] != "OK" {
				t.Fatal(response)
			}
			response = rpc("keepalived", map[string]interface{}{})
			if response["result"].(map[string]interface{})["status"] != "KEEPALIVED" {
				t.Fatal(response)
			}
			zero := strings.Repeat("00", 64)
			if err = writeMessage(qs, message{JobResult: &result{Status: "completed", JobID: qjob.NewJob.JobID, Work: &zero}}); err != nil {
				t.Fatal(err)
			}
			if m, err := readMessage(qs); err != nil || m.NewJob == nil {
				t.Fatalf("QUIC share continuation %v", err)
			}
			limit := time.Now().Add(time.Second)
			for (mr.HGet("MIXED:shares:roundCurrent", "stratum") == "" || mr.HGet("MIXED:shares:roundCurrent", "native") == "") && time.Now().Before(limit) {
				time.Sleep(time.Millisecond)
			}
			if got := mr.HGet("MIXED:shares:roundCurrent", "stratum"); got != "1" {
				t.Fatalf("Stratum share credit %q", got)
			}
			if got := mr.HGet("MIXED:shares:roundCurrent", "native"); got != "1" {
				t.Fatalf("QUIC share credit %q", got)
			}
		})
	}
}
func TestTransportConfig(t *testing.T) {
	for _, p := range []*config.PortOptions{nil, {Protocol: "unknown", Diff: 1}, {Protocol: "quic", Diff: 1}, {Protocol: "stratum", Diff: 1.5}, {Protocol: "stratum", Diff: math.Exp2(64)}, {Protocol: "stratum", Diff: 1, VarDiff: &config.VarDiffOptions{}}} {
		if validatePort(p) == nil {
			t.Fatal("accepted invalid port", p)
		}
	}
	if err := validatePort(&config.PortOptions{Protocol: "stratum", Diff: 1}); err != nil {
		t.Fatal(err)
	}
}
