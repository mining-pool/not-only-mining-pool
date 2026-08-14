package kawpow

import (
	"encoding/hex"
	"math/big"
	"net"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/engine"

	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/jobs"
	"github.com/mining-pool/not-only-mining-pool/types"
	"github.com/sencha-dev/powkit/kawpow"
)

func TestSerializeKawpowInput(t *testing.T) {
	prev := make([]byte, 32)
	prev[0] = 0xaa
	merkle := make([]byte, 32)
	merkle[0] = 0xbb

	b := serializeKawpowInput(0x30000000, prev, merkle, 0x5f5e0f00, "1d00ffff", 12345)
	if len(b) != 80 {
		t.Fatalf("kawpow input must be 80 bytes, got %d", len(b))
	}
	// version LE
	if hex.EncodeToString(b[0:4]) != "00000030" {
		t.Fatalf("version LE wrong: %x", b[0:4])
	}
	if b[4] != 0xaa || b[36] != 0xbb {
		t.Fatal("prevhash/merkleroot placement wrong")
	}
	// bits: display "1d00ffff" -> LE ffff001d
	if hex.EncodeToString(b[72:76]) != "ffff001d" {
		t.Fatalf("bits LE wrong: %x", b[72:76])
	}
	// height 12345 = 0x3039 LE
	if hex.EncodeToString(b[76:80]) != "39300000" {
		t.Fatalf("height LE wrong: %x", b[76:80])
	}
}

func TestSeedHash(t *testing.T) {
	// epoch 0: seed is 32 zero bytes
	if hex.EncodeToString(seedHash(7499)) != hex.EncodeToString(make([]byte, 32)) {
		t.Fatal("epoch-0 seed must be all zeroes")
	}
	// epoch 1: keccak256 of 32 zero bytes (well-known constant)
	if hex.EncodeToString(seedHash(7500)) != "290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563" {
		t.Fatalf("epoch-1 seed wrong: %x", seedHash(7500))
	}
}

func TestTargetMath(t *testing.T) {
	// diff 1 -> exactly diff1
	if engine.TargetFromDiff(diff1, 1).Cmp(diff1) != 0 {
		t.Fatal("engine.TargetFromDiff(diff1, 1) must equal diff1")
	}
	// higher diff -> smaller target
	if engine.TargetFromDiff(diff1, 2).Cmp(engine.TargetFromDiff(diff1, 1)) >= 0 {
		t.Fatal("higher diff must shrink the target")
	}
	if got := engine.TargetHex(big.NewInt(0xff)); len(got) != 64 || got[62:] != "ff" {
		t.Fatalf("targetHex padding wrong: %q", got)
	}
	if d := engine.DiffFromValue(diff1, new(big.Int).Set(diff1)); d < 0.999 || d > 1.001 {
		t.Fatalf("diffFromDigest(diff1) should be ~1, got %v", d)
	}
}

func TestFullHeaderLayout(t *testing.T) {
	j := &job{header80: make([]byte, 80)}
	mix := make([]byte, 32)
	mix[0] = 0xcc // most-significant in kawpow order

	h := j.fullHeader(0x0102030405060708, mix)
	if len(h) != 120 {
		t.Fatalf("full header must be 120 bytes, got %d", len(h))
	}
	// nonce64 LE
	if hex.EncodeToString(h[80:88]) != "0807060504030201" {
		t.Fatalf("nonce LE wrong: %x", h[80:88])
	}
	// mix_hash stored in internal (reversed) order -> 0xcc lands at the END
	if h[119] != 0xcc || h[88] != 0 {
		t.Fatalf("mix must be reversed into the header: %x", h[88:120])
	}
}

type fakeSession struct{ diff float64 }

func (f fakeSession) ExtraNonce1() []byte { return nil }
func (f fakeSession) Difficulty() float64 { return f.diff }
func (f fakeSession) WorkerName() string  { return "tester" }
func (f fakeSession) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}
func (f fakeSession) Send(string, []interface{}) error { return nil }

// TestOnSubmitKnownVector drives the full OnSubmit verification path with
// powkit's real kawpow test vector (height 0, zero hash, zero nonce). It
// generates the ~16MB epoch-0 light cache on first run, so it can take a few
// seconds; network-target submission is not exercised (target set below any
// digest) because that would require a live node.
func TestOnSubmitKnownVector(t *testing.T) {
	if testing.Short() {
		t.Skip("generates a 16MB kawpow cache; skipped in -short")
	}

	headerHash := make([]byte, 32) // vector: all-zero hash
	e := New()
	e.pow = kawpow.NewRavencoin()
	j := &job{
		id:         "1",
		headerHash: headerHash,
		height:     0,
		seen:       map[string]struct{}{},
		inner: &jobs.Job{
			Target:           big.NewInt(1), // unreachably hard -> never tries submitblock
			GetBlockTemplate: &daemons.GetBlockTemplate{},
		},
	}
	e.cur = j
	e.history[j.id] = j

	sess := fakeSession{diff: 1e-40} // accept virtually any digest as a share

	params := []interface{}{
		"tester", "1",
		"0x0000000000000000",
		"0x" + hex.EncodeToString(headerHash),
		"0x6e97b47b134fda0c7888802988e1a373affeb28bcd813b6e9a0fc669c935d03a", // vector mix
	}

	share := e.OnSubmit(sess, params)
	if share.ErrorCode != 0 {
		t.Fatalf("known-good vector rejected: %v (%s)", int(share.ErrorCode), share.ErrorCode.String())
	}

	// duplicate must be rejected
	if e.OnSubmit(sess, params).ErrorCode != types.ErrDuplicateShare {
		t.Fatal("duplicate nonce must be rejected")
	}

	// wrong mix must be rejected
	bad := []interface{}{
		"tester", "1",
		"0x0000000000000001",
		"0x" + hex.EncodeToString(headerHash),
		"0x6e97b47b134fda0c7888802988e1a373affeb28bcd813b6e9a0fc669c935d03a",
	}
	if e.OnSubmit(sess, bad).ErrorCode == 0 {
		t.Fatal("wrong mix for nonce must be rejected")
	}
}
