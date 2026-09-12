package quantus

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mining-pool/not-only-mining-pool/bans"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/storage"
	"github.com/mining-pool/not-only-mining-pool/stratum"
	"github.com/mining-pool/not-only-mining-pool/types"
	"github.com/quic-go/quic-go"
)

func certificate(t *testing.T) (tls.Certificate, *config.TLSServerOptions, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	opts := &config.TLSServerOptions{CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem")}
	if err = os.WriteFile(opts.CertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(opts.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0600); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(der)
	return cert, opts, hex.EncodeToString(hash[:])
}
func deadlineContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
		var zero T
		return zero
	}
}

type session struct{ diff float64 }

func (s session) ExtraNonce1() []byte              { return nil }
func (s session) Difficulty() float64              { return s.diff }
func (s session) WorkerName() string               { return "account.rig" }
func (s session) RemoteAddr() net.Addr             { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234} }
func (s session) Send(string, []interface{}) error { return nil }

func TestNodeLifecycleAndShares(t *testing.T) {
	cert, poolTLS, pin := certificate(t)
	node, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{alpn}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	dir := t.TempDir()
	tokenFile, pinFile := filepath.Join(dir, "token"), filepath.Join(dir, "pin")
	os.WriteFile(tokenFile, []byte("secret\n"), 0600)
	os.WriteFile(pinFile, []byte(pin+"\n"), 0600)
	e := New()
	defer e.Close()
	ctx := deadlineContext(t)
	if err = e.Init(&config.Options{DisablePayment: true, Quantus: &config.QuantusOptions{NodeAddress: node.Addr().String(), AuthTokenFile: tokenFile, TLSCertSHA256File: pinFile}, Ports: map[int]*config.PortOptions{3333: {Diff: 1, TLS: poolTLS}}}); err != nil {
		t.Fatal(err)
	}
	nc, err := node.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.CloseWithError(0, "")
	ns, err := nc.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ns.SetDeadline(time.Now().Add(10 * time.Second))
	ready, err := readMessage(ns)
	if err != nil || ready.Ready == nil || ready.Ready.Token != "secret" {
		t.Fatalf("handshake %v %v", ready, err)
	}
	events := make(chan struct{}, 10)
	done := make(chan error, 1)
	go func() { done <- e.Watch(func() { events <- struct{}{} }) }()
	jobMsg := message{NewJob: &request{JobID: "job1", MiningHash: strings.Repeat("00", 32), Difficulty: "1"}}
	if err = writeMessage(ns, jobMsg); err != nil {
		t.Fatal(err)
	}
	receive(t, events)
	if p := e.JobParamsForDifficulty(123); len(p) != 3 || p[2] != "123" {
		t.Fatal(p)
	}
	nonce := strings.Repeat("00", 64)
	share := e.OnSubmit(session{1}, []interface{}{"account.rig", "job1", nonce})
	if share.ErrorCode != 0 || share.Diff <= 1 || share.BlockHash != "" || share.TxHash != "" {
		t.Fatalf("share %+v", share)
	}
	result, err := readMessage(ns)
	if err != nil || result.JobResult == nil || *result.JobResult.Work != nonce || *result.JobResult.Nonce != "0" {
		t.Fatalf("submission %+v %v", result, err)
	}
	if s := e.OnSubmit(session{1}, []interface{}{"account.rig", "job1", nonce}); s.ErrorCode != types.ErrDuplicateShare {
		t.Fatal(s)
	}
	// Duplicate notification must not make the same seal payable twice.
	writeMessage(ns, jobMsg)
	receive(t, events)
	if s := e.OnSubmit(session{1}, []interface{}{"account.rig", "job1", nonce}); s.ErrorCode != types.ErrDuplicateShare {
		t.Fatal(s)
	}
	// A new tip makes the old job stale.
	jobMsg.NewJob = &request{JobID: "job2", MiningHash: strings.Repeat("01", 32), Difficulty: maxTarget.String()}
	writeMessage(ns, jobMsg)
	receive(t, events)
	if s := e.OnSubmit(session{1}, []interface{}{"account.rig", "job1", nonce}); s.ErrorCode != types.ErrJobNotFound {
		t.Fatal(s)
	}
	for _, p := range [][]interface{}{nil, {"a", "job2", "bad"}, {"a", "job2", 42}} {
		if s := e.OnSubmit(session{1}, p); s.ErrorCode != types.ErrIncorrectNonceSize {
			t.Fatal(s)
		}
	}
	if s := e.OnSubmit(session{math.MaxFloat64}, []interface{}{"a", "job2", nonce}); s.ErrorCode != types.ErrLowDiffShare {
		t.Fatal(s)
	}
	// A rejected low-difficulty share is not entered in the duplicate set.
	if s := e.OnSubmit(session{1}, []interface{}{"a", "job2", nonce}); s.ErrorCode != 0 {
		t.Fatal(s)
	}
	nc.CloseWithError(0, "restart")
	receive(t, events)
	if e.JobParamsForDifficulty(1) != nil {
		t.Fatal("disconnected node still advertises work")
	}
	// Reconnect and authenticate again, then consume pushed work.
	nc2, err := node.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer nc2.CloseWithError(0, "")
	ns2, err := nc2.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ns2.SetDeadline(time.Now().Add(10 * time.Second))
	ready, err = readMessage(ns2)
	if err != nil || ready.Ready == nil {
		t.Fatalf("reconnect %v", err)
	}
	writeMessage(ns2, jobMsg)
	receive(t, events)
	if s := e.OnSubmit(session{1}, []interface{}{"a", "job2", nonce}); s.ErrorCode != types.ErrDuplicateShare {
		t.Fatal("reconnect erased duplicate history", s)
	}
	e.Close()
	if err = receive(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestNativeMinerTransport(t *testing.T) {
	_, serverTLS, pin := certificate(t)
	e := New()
	defer e.Close()
	listener, err := e.Listen(0, &config.PortOptions{Diff: 1, TLS: serverTLS})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	tlsConfig, _ := pinnedTLS(pin)
	ctx := deadlineContext(t)
	address := net.JoinHostPort("127.0.0.1", fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))
	c, err := quic.DialAddr(ctx, address, tlsConfig, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseWithError(0, "")
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	s, err := c.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.SetDeadline(time.Now().Add(10 * time.Second))
	ready := message{}
	ready.Ready = &struct {
		Token string `json:"token"`
	}{"account.rig"}
	if err = writeMessage(s, ready); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	raw, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var rpc struct {
		Method string
		Params []string
	}
	if err = json.Unmarshal(raw, &rpc); err != nil {
		t.Fatal(err)
	}
	if rpc.Method != "mining.authorize" || rpc.Params[0] != "account.rig" {
		t.Fatal(string(raw))
	}
	notify := `{"method":"mining.notify","params":["a","` + strings.Repeat("00", 32) + `","100"]}` + "\n"
	// net.Conn must tolerate partial writes and coalesced JSON lines.
	if _, err = conn.Write([]byte(notify[:10])); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write([]byte(notify[10:])); err != nil {
		t.Fatal(err)
	}
	m, err := readMessage(s)
	if err != nil || m.NewJob == nil || m.NewJob.Difficulty != "100" {
		t.Fatalf("job %v %v", m, err)
	}
	nonce := strings.Repeat("00", 64)
	if err = writeMessage(s, message{JobResult: &result{Status: "completed", JobID: "a", Work: &nonce}}); err != nil {
		t.Fatal(err)
	}
	raw, err = reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(raw, &rpc)
	if rpc.Method != "mining.submit" || rpc.Params[2] != nonce {
		t.Fatal(string(raw))
	}
	conn.Write([]byte("{\"id\":1,\"result\":true}\n{\"id\":2,\"result\":true}\n"))
	m, err = readMessage(s)
	if err != nil || m.NewJob == nil || m.NewJob.JobID != "a" {
		t.Fatalf("miner not restarted %v", err)
	}
}

func TestCertificatePin(t *testing.T) {
	cert, _, pin := certificate(t)
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pinnedTLS(pin)
	if err != nil {
		t.Fatal(err)
	}
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{parsed}}
	if err = cfg.VerifyConnection(state); err != nil {
		t.Fatal(err)
	}
	wrong, _ := pinnedTLS(strings.Repeat("00", 32))
	if wrong.VerifyConnection(state) == nil {
		t.Fatal("accepted wrong certificate")
	}
	if cfg.VerifyConnection(tls.ConnectionState{}) == nil {
		t.Fatal("accepted missing certificate")
	}
	if _, err = pinnedTLS("invalid"); err == nil {
		t.Fatal("accepted invalid pin")
	}
}

func TestStrictNetworkTarget(t *testing.T) {
	e := New()
	defer e.Close()
	j, err := parseJob(&request{JobID: "boundary", MiningHash: strings.Repeat("00", 32), Difficulty: "1"})
	if err != nil {
		t.Fatal(err)
	}
	hash := NonceHash(j.header, [64]byte{})
	j.target = new(big.Int).SetBytes(hash[:])
	e.cur = j
	// Equality is NOT a block. A <= comparison would attempt a write on the
	// absent upstream stream and fail this regression test.
	s := e.OnSubmit(session{1}, []interface{}{"account.rig", "boundary", strings.Repeat("00", 64)})
	if s.ErrorCode != 0 {
		t.Fatal(s)
	}
}
func TestInvalidConfiguration(t *testing.T) {
	e := New()
	defer e.Close()
	if e.Init(&config.Options{}) == nil {
		t.Fatal("accepted missing configuration")
	}
	if e.Init(&config.Options{Quantus: &config.QuantusOptions{}}) == nil {
		t.Fatal("accepted automatic payments")
	}
	for _, d := range []float64{0, -1, math.NaN(), math.Inf(1), math.MaxFloat64} {
		if validDiff(d) {
			t.Fatal("accepted", d)
		}
	}
}

func TestNativeMinerShareAccounting(t *testing.T) {
	_, serverTLS, pin := certificate(t)
	e := New()
	defer e.Close()
	e.cur, _ = parseJob(&request{JobID: "accounting", MiningHash: strings.Repeat("00", 32), Difficulty: maxTarget.String()})
	listener, err := e.Listen(0, &config.PortOptions{Diff: 1, TLS: serverTLS})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	tlsConfig, _ := pinnedTLS(pin)
	c, err := quic.DialAddr(deadlineContext(t), net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), tlsConfig, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseWithError(0, "")
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	s, err := c.OpenStreamSync(deadlineContext(t))
	if err != nil {
		t.Fatal(err)
	}
	s.SetDeadline(time.Now().Add(10 * time.Second))
	opts := &config.Options{Ports: map[int]*config.PortOptions{port: {Diff: 1}}, Banning: &config.BanningOptions{CheckThreshold: 100, InvalidPercent: 50}}
	client := stratum.NewStratumClient([]byte{1}, conn, opts, nil, bans.NewBanningManager(opts.Banning))
	client.Engine = e
	mr := miniredis.RunT(t)
	redisPort, _ := strconv.Atoi(mr.Port())
	client.DB = storage.NewStorage("QTC", &config.RedisOptions{Host: mr.Host(), Port: redisPort})
	defer client.DB.Close()
	ready := message{}
	ready.Ready = &struct {
		Token string `json:"token"`
	}{"account.rig"}
	if err = writeMessage(s, ready); err != nil {
		t.Fatal(err)
	}
	dispatch := func() {
		t.Helper()
		raw, err := client.SocketBufIO.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var r daemons.JsonRpcRequest
		if err = json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		client.HandleMessage(&r)
	}
	dispatch()
	m, err := readMessage(s)
	if err != nil || m.NewJob == nil || m.NewJob.Difficulty != "1" {
		t.Fatalf("authorize/work: %v %v", m, err)
	}
	nonce := strings.Repeat("00", 64)
	if err = writeMessage(s, message{JobResult: &result{Status: "completed", JobID: m.NewJob.JobID, Work: &nonce}}); err != nil {
		t.Fatal(err)
	}
	dispatch()
	m, err = readMessage(s)
	if err != nil || m.NewJob == nil {
		t.Fatalf("share reply did not restart miner: %v", err)
	}
	limit := time.Now().Add(time.Second)
	for mr.HGet("QTC:shares:roundCurrent", "account") == "" && time.Now().Before(limit) {
		time.Sleep(time.Millisecond)
	}
	if got := mr.HGet("QTC:shares:roundCurrent", "account"); got != "1" {
		t.Fatalf("credited %q, want assigned difficulty 1", got)
	}
	if ok, _ := mr.SIsMember("QTC:miner:account:rigs", "rig"); !ok {
		t.Fatal("missing rig accounting")
	}
	if client.Shares.Valid != 1 {
		t.Fatal("missing valid share")
	}
	if client.SocketClosedEvent == nil {
		t.Fatal("client cannot release disconnected session")
	}
}
