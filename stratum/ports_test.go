package stratum

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mining-pool/not-only-mining-pool/bans"
	"github.com/mining-pool/not-only-mining-pool/config"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// writeCert creates a self-signed certificate for 127.0.0.1 named cn.
func writeCert(t *testing.T, dir, cn string) *config.TLSServerOptions {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certFile := filepath.Join(dir, cn+".crt")
	keyFile := filepath.Join(dir, cn+".key")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return &config.TLSServerOptions{CertFile: certFile, KeyFile: keyFile}
}

func portServer(t *testing.T, ports map[int]*config.PortOptions) *Server {
	t.Helper()
	opts := &config.Options{Ports: ports, ConnectionTimeout: 600}
	ss := NewStratumServer(opts, nil, bans.NewBanningManager(nil))
	ss.Live = config.NewLive(opts)
	if started := ss.startPorts(ports); len(started) != len(ports) {
		t.Fatalf("started %v of %v", started, ports)
	}
	t.Cleanup(func() {
		ss.portsMu.Lock()
		for _, pl := range ss.listeners {
			pl.close()
		}
		ss.portsMu.Unlock()
		for _, c := range ss.snapshotClients() {
			c.closeSocket()
		}
	})
	return ss
}

func dial(t *testing.T, port int) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second)
	if err != nil {
		t.Fatalf("dial %d: %v", port, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// clientOn waits for the server to register n clients on port.
func clientsOn(t *testing.T, ss *Server, port, n int) []*Client {
	t.Helper()
	for i := 0; i < 200; i++ {
		var found []*Client
		for _, c := range ss.snapshotClients() {
			if c.Port == port {
				found = append(found, c)
			}
		}
		if len(found) == n {
			return found
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d clients on port %d", n, port)
	return nil
}

func applyPorts(t *testing.T, ss *Server, next map[int]*config.PortOptions) []PortChange {
	t.Helper()
	p, err := ss.PreparePorts(next)
	if err != nil {
		t.Fatal(err)
	}
	changes, errs := p.Commit()
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return changes
}

func TestReloadAddsUpdatesAndRemovesPorts(t *testing.T) {
	a, b := freePort(t), freePort(t)
	ss := portServer(t, map[int]*config.PortOptions{a: {Diff: 8}})
	dial(t, a)
	old := clientsOn(t, ss, a, 1)[0]

	changes := applyPorts(t, ss, map[int]*config.PortOptions{a: {Diff: 16}, b: {Diff: 32}})
	want := map[PortChange]bool{{a, "updated"}: true, {b, "added"}: true}
	if len(changes) != 2 || !want[changes[0]] || !want[changes[1]] {
		t.Fatalf("changes: %v", changes)
	}
	// A connected miner keeps the settings it connected with.
	if old.portDefaultDiff() != 8 {
		t.Fatalf("existing connection changed to diff %v", old.portDefaultDiff())
	}
	dial(t, a)
	dial(t, b)
	var fresh *Client
	for _, c := range clientsOn(t, ss, a, 2) {
		if c != old {
			fresh = c
		}
	}
	if fresh.portDefaultDiff() != 16 || clientsOn(t, ss, b, 1)[0].portDefaultDiff() != 32 {
		t.Fatal("new connections must use the reloaded port settings")
	}

	applyPorts(t, ss, map[int]*config.PortOptions{b: {Diff: 32}})
	select {
	case <-old.SocketClosedEvent:
	case <-time.After(time.Second):
		t.Fatal("miners on a removed port must be disconnected")
	}
	if _, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(a), 200*time.Millisecond); err == nil {
		t.Fatal("removed port still accepts connections")
	}
	if got := ss.Ports(); len(got) != 1 || got[0] != b {
		t.Fatalf("serving %v", got)
	}
}

func TestFailedPrepareLeavesPortsUntouched(t *testing.T) {
	a, b := freePort(t), freePort(t)
	ss := portServer(t, map[int]*config.PortOptions{a: {Diff: 8}})
	busy, err := net.Listen("tcp", ":"+strconv.Itoa(freePort(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	taken := busy.Addr().(*net.TCPAddr).Port

	// b binds fine but the taken port fails: the whole change is rejected and
	// b must be released again.
	if _, err := ss.PreparePorts(map[int]*config.PortOptions{a: {Diff: 1}, b: {Diff: 1}, taken: {Diff: 1}}); err == nil {
		t.Fatal("binding a port in use must fail")
	}
	if l, err := net.Listen("tcp", ":"+strconv.Itoa(b)); err != nil {
		t.Fatal("aborted reload kept a port bound: ", err)
	} else {
		l.Close()
	}
	if got := ss.Ports(); len(got) != 1 || got[0] != a || ss.portOptions(a).Diff != 8 {
		t.Fatal("failed reload changed the running ports")
	}
	for _, bad := range []map[int]*config.PortOptions{
		{},
		{a: {Diff: 0}},
		{a: {Diff: 1, VarDiff: &config.VarDiffOptions{MinDiff: 2, MaxDiff: 1, TargetTime: 1, RetargetTime: 1}}},
		{a: {Diff: 1, TLS: &config.TLSServerOptions{CertFile: "/missing.crt", KeyFile: "/missing.key"}}},
	} {
		if _, err := ss.PreparePorts(bad); err == nil {
			t.Fatalf("accepted invalid ports %v", bad)
		}
	}
}

func handshake(t *testing.T, port int) (*tls.Conn, string) {
	t.Helper()
	c, err := tls.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, c.ConnectionState().PeerCertificates[0].Subject.CommonName
}

func TestCertificateRenewalKeepsConnections(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	first := writeCert(t, dir, "first")
	ss := portServer(t, map[int]*config.PortOptions{port: {Diff: 1, TLS: first}})
	conn, cn := handshake(t, port)
	if cn != "first" {
		t.Fatal(cn)
	}
	client := clientsOn(t, ss, port, 1)[0]

	// Renewal in place: same paths, new content.
	renewed := writeCert(t, dir, "renewed")
	_ = os.Rename(renewed.CertFile, first.CertFile)
	_ = os.Rename(renewed.KeyFile, first.KeyFile)
	if changes := applyPorts(t, ss, map[int]*config.PortOptions{port: {Diff: 1, TLS: first}}); len(changes) != 0 {
		t.Fatalf("a renewal is not a port change: %v", changes)
	}
	if _, cn := handshake(t, port); cn != "renewed" {
		t.Fatalf("new handshakes still get %q", cn)
	}
	select {
	case <-client.SocketClosedEvent:
		t.Fatal("certificate renewal disconnected a miner")
	default:
	}
	if _, err := conn.Write([]byte("{}\n")); err != nil {
		t.Fatal("existing TLS connection broken: ", err)
	}
}

func TestTogglingTLSRebindsThePort(t *testing.T) {
	port := freePort(t)
	ss := portServer(t, map[int]*config.PortOptions{port: {Diff: 1}})
	dial(t, port)
	plain := clientsOn(t, ss, port, 1)[0]

	changes := applyPorts(t, ss, map[int]*config.PortOptions{port: {Diff: 1, TLS: writeCert(t, t.TempDir(), "tls")}})
	if len(changes) != 1 || changes[0] != (PortChange{port, "rebound"}) {
		t.Fatalf("changes: %v", changes)
	}
	select {
	case <-plain.SocketClosedEvent:
	case <-time.After(time.Second):
		t.Fatal("plain connections must be dropped when the port switches to TLS")
	}
	if _, cn := handshake(t, port); cn != "tls" {
		t.Fatal("port does not speak TLS after the reload")
	}
}
