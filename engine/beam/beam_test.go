package beam

import (
	"net"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/types"
)

// The first BeamHash III vector from powkit's test suite: header(40) = input(32)
// || nonce(8), with its valid 104-byte solution.
var (
	beamHeader = []byte{
		0xfc, 0x40, 0x99, 0x6a, 0x51, 0x8c, 0x22, 0x13,
		0x84, 0xc9, 0xf2, 0x54, 0x2c, 0xa8, 0x11, 0xcd,
		0x66, 0xc4, 0xcc, 0xdd, 0xb0, 0x01, 0xef, 0x40,
		0xb9, 0xf9, 0xba, 0x05, 0x9c, 0x20, 0x35, 0x2e,
		0xb3, 0x2c, 0x7d, 0x4f, 0x07, 0xa3, 0x00, 0x1c,
	}
	beamSoln = []byte{
		0x0f, 0xc8, 0x1c, 0x68, 0x4b, 0xe2, 0x29, 0xc3,
		0x6b, 0x84, 0x4e, 0xf8, 0x29, 0x9a, 0x97, 0x44,
		0xdb, 0xb8, 0x72, 0x72, 0x76, 0xbf, 0xf8, 0xcb,
		0xd6, 0x10, 0xfa, 0x74, 0x14, 0xfb, 0x6c, 0xfd,
		0x67, 0xb9, 0x25, 0x86, 0xf8, 0x4f, 0x8b, 0xff,
		0xae, 0xeb, 0x99, 0x26, 0x69, 0x94, 0xd7, 0x9d,
		0xa3, 0xfb, 0x02, 0x6a, 0x24, 0x12, 0x8b, 0x84,
		0x90, 0x1f, 0x24, 0x4b, 0x08, 0xee, 0x6b, 0x6b,
		0x95, 0x43, 0x72, 0xfc, 0xb0, 0xa7, 0xd3, 0x33,
		0x18, 0xda, 0x6b, 0xf1, 0x85, 0x4a, 0xe4, 0x8f,
		0x94, 0xfe, 0x8a, 0xf2, 0xd3, 0x14, 0x7b, 0xdc,
		0x73, 0x02, 0xcc, 0x12, 0xda, 0xa1, 0xa3, 0x06,
		0x51, 0x11, 0x22, 0xa7, 0x00, 0x00, 0x00, 0x00,
	}
)

func hexs(b []byte) string {
	const h = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = h[c>>4]
		out[i*2+1] = h[c&0xf]
	}
	return string(out)
}

func TestHandleJobLine(t *testing.T) {
	e := New()
	if !e.handleLine([]byte(`{"method":"job","id":"212","input":"` + hexs(beamHeader[:32]) + `","difficulty":3441671469}`)) {
		t.Fatal("job line should register new work")
	}
	j := e.currentJob()
	if j == nil || j.id != "212" || j.difficulty != 3441671469 || len(j.input) != 32 {
		t.Fatalf("job not parsed: %+v", j)
	}
	// login result must set the nonce prefix
	if e.handleLine([]byte(`{"method":"result","id":"login","code":0,"nonceprefix":"ab4e3a"}`)) {
		t.Fatal("result line is not new work")
	}
	if hexs(e.noncePrefix) != "ab4e3a" {
		t.Fatalf("nonce prefix not stored: %x", e.noncePrefix)
	}
}

type fakeSession struct{}

func (fakeSession) ExtraNonce1() []byte  { return nil }
func (fakeSession) Difficulty() float64  { return 1 }
func (fakeSession) WorkerName() string   { return "w" }
func (fakeSession) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
func (fakeSession) Send(string, []interface{}) error { return nil }

// TestOnSubmitBeamVector runs the real BeamHash III vector through OnSubmit. The
// node forward is stubbed by leaving enc nil (send errors are logged, not
// fatal), so this exercises the equihash verification + share bookkeeping.
func TestOnSubmitBeamVector(t *testing.T) {
	input := beamHeader[:32]
	nonce := beamHeader[32:40]

	e := New()
	j := &job{id: "1", input: input, difficulty: 1000, seen: map[string]struct{}{}}
	e.cur = j
	e.history["1"] = j

	share := e.OnSubmit(fakeSession{}, []interface{}{"w", "1", hexs(nonce), hexs(beamSoln)})
	if share.ErrorCode != 0 {
		t.Fatalf("valid Beam solution rejected: %s", share.ErrorCode.String())
	}
	if share.Diff != 1000 {
		t.Fatalf("share diff should track job difficulty, got %v", share.Diff)
	}

	// duplicate
	if e.OnSubmit(fakeSession{}, []interface{}{"w", "1", hexs(nonce), hexs(beamSoln)}).ErrorCode != types.ErrDuplicateShare {
		t.Fatal("duplicate solution must be rejected")
	}

	// wrong solution length
	if e.OnSubmit(fakeSession{}, []interface{}{"w", "1", hexs(nonce), "00"}).ErrorCode != types.ErrIncorrectNonceSize {
		t.Fatal("short output must be rejected")
	}

	// unknown job
	if e.OnSubmit(fakeSession{}, []interface{}{"w", "9", hexs(nonce), hexs(beamSoln)}).ErrorCode != types.ErrJobNotFound {
		t.Fatal("unknown job must be rejected")
	}

	// a forged solution (flip a byte) must fail equihash verification
	bad := append([]byte{}, beamSoln...)
	bad[0] ^= 0xff
	nonce2 := append([]byte{}, nonce...)
	nonce2[0] ^= 0xff // fresh key to avoid the dedup path
	if e.OnSubmit(fakeSession{}, []interface{}{"w", "1", hexs(nonce2), hexs(bad)}).ErrorCode == 0 {
		t.Fatal("forged solution must be rejected")
	}
}
