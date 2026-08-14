package alephium

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"net"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/types"
	"lukechampine.com/blake3"
)

// buildJobsPayload serializes a Jobs payload the way the node does, so the
// parser is tested against the documented wire format.
func buildJobsPayload(jobs []*job) []byte {
	var b []byte
	var cnt [4]byte
	binary.BigEndian.PutUint32(cnt[:], uint32(len(jobs)))
	b = append(b, cnt[:]...)
	put32 := func(v uint32) {
		var x [4]byte
		binary.BigEndian.PutUint32(x[:], v)
		b = append(b, x[:]...)
	}
	for _, j := range jobs {
		put32(j.fromGroup)
		put32(j.toGroup)
		b = appendBlob(b, j.headerBlob)
		b = appendBlob(b, j.txsBlob)
		b = appendBlob(b, j.target.Bytes())
		put32(uint32(j.height))
	}
	return b
}

func TestParseJobsRoundTrip(t *testing.T) {
	in := []*job{
		{fromGroup: 0, toGroup: 1, headerBlob: []byte{1, 2, 3}, txsBlob: []byte{4, 5}, target: big.NewInt(0x123456), height: 100},
		{fromGroup: 2, toGroup: 3, headerBlob: bytes.Repeat([]byte{0xaa}, 40), txsBlob: []byte{9}, target: big.NewInt(0xffff), height: 101},
	}
	payload := buildJobsPayload(in)

	var n int
	got, err := parseJobs(payload, func() string { n++; return "j" + string(rune('0'+n)) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(got))
	}
	if got[0].fromGroup != 0 || got[0].toGroup != 1 || !bytes.Equal(got[0].headerBlob, []byte{1, 2, 3}) {
		t.Fatalf("job0 fields wrong: %+v", got[0])
	}
	if got[1].height != 101 || got[1].target.Cmp(big.NewInt(0xffff)) != 0 {
		t.Fatalf("job1 fields wrong: %+v", got[1])
	}
	if len(got[1].headerBlob) != 40 {
		t.Fatalf("job1 headerBlob len wrong: %d", len(got[1].headerBlob))
	}
}

func TestFrameRoundTrip(t *testing.T) {
	payload := []byte{0xde, 0xad, 0xbe, 0xef}
	frame := buildFrame(msgSubmitBlock, payload)

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() { _, _ = a.Write(frame) }()

	ver, typ, got, err := readFrame(b)
	if err != nil {
		t.Fatal(err)
	}
	if ver != protocolVersion || typ != msgSubmitBlock || !bytes.Equal(got, payload) {
		t.Fatalf("frame round trip wrong: ver=%d typ=%d payload=%x", ver, typ, got)
	}
}

func TestDoubleBlake3(t *testing.T) {
	// double blake3 must equal blake3(blake3(x)) using the library directly
	x := []byte("alephium pow input")
	inner := blake3.Sum256(x)
	outer := blake3.Sum256(inner[:])
	if !bytes.Equal(doubleBlake3(x), outer[:]) {
		t.Fatal("doubleBlake3 mismatch vs blake3(blake3(x))")
	}
	if len(doubleBlake3(x)) != 32 {
		t.Fatal("pow hash must be 32 bytes")
	}
}

type fakeSession struct {
	en1  []byte
	diff float64
}

func (f fakeSession) ExtraNonce1() []byte { return f.en1 }
func (f fakeSession) Difficulty() float64 { return f.diff }
func (f fakeSession) WorkerName() string  { return "w" }
func (f fakeSession) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}
func (f fakeSession) Send(string, []interface{}) error { return nil }

// TestOnSubmitPoWPath drives OnSubmit: a nonce whose doubleBlake3 lands under
// the (loose) share target is accepted; too-hard target -> low-diff; duplicate
// and short nonce -> rejected. Network submit is not exercised (conn is nil, so
// the job target is set unreachable).
func TestOnSubmitPoWPath(t *testing.T) {
	headerBlob := bytes.Repeat([]byte{0x11}, 40)
	e := New()
	j := &job{
		id:         "1",
		headerBlob: headerBlob,
		txsBlob:    []byte{0x01},
		target:     big.NewInt(1), // unreachable network target
		height:     42,
		seen:       map[string]struct{}{},
	}
	e.history["1"] = j

	nonce := bytes.Repeat([]byte{0x07}, nonceSize)
	nonceHex := hex.EncodeToString(nonce)

	// very loose share target -> any hash passes as a share
	sess := fakeSession{diff: 1e-30}
	share := e.OnSubmit(sess, []interface{}{"w", "1", nonceHex})
	if share.ErrorCode != 0 {
		t.Fatalf("valid share rejected: %s", share.ErrorCode.String())
	}
	if share.Diff <= 0 {
		t.Fatal("share diff must be positive")
	}

	if e.OnSubmit(sess, []interface{}{"w", "1", nonceHex}).ErrorCode != types.ErrDuplicateShare {
		t.Fatal("duplicate nonce must be rejected")
	}
	if e.OnSubmit(sess, []interface{}{"w", "1", "00"}).ErrorCode != types.ErrIncorrectNonceSize {
		t.Fatal("short nonce must be rejected")
	}
	if e.OnSubmit(sess, []interface{}{"w", "99", nonceHex}).ErrorCode != types.ErrJobNotFound {
		t.Fatal("unknown job must be rejected")
	}

	// impossibly hard share target -> low-diff
	hard := fakeSession{diff: 1e40}
	n2 := bytes.Repeat([]byte{0x08}, nonceSize)
	if e.OnSubmit(hard, []interface{}{"w", "1", hex.EncodeToString(n2)}).ErrorCode != types.ErrLowDiffShare {
		t.Fatal("hard target must yield low-diff share")
	}
}
