//go:build randomx
// +build randomx

package cryptonote

import (
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"net"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/types"
)

type fakeSession struct{ diff float64 }

func (f fakeSession) ExtraNonce1() []byte { return nil }
func (f fakeSession) Difficulty() float64 { return f.diff }
func (f fakeSession) WorkerName() string  { return "tester" }
func (f fakeSession) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}
func (f fakeSession) Send(string, []interface{}) error { return nil }

// TestOnSubmitRandomXPlumbing drives the full OnSubmit path with a REAL
// RandomX light VM over a synthetic job: an honest share (result computed with
// the same VM) must be accepted; duplicates and forged results must not.
// RandomX correctness itself is pinned by the official vectors in
// third_party/go-randomx.
func TestOnSubmitRandomXPlumbing(t *testing.T) {
	blob := buildSyntheticBlob(t, nil)
	p, err := parseBlockBlob(blob)
	if err != nil {
		t.Fatal(err)
	}

	pow, err := newPowHasher()
	if err != nil {
		t.Fatal(err)
	}
	defer pow.Close()

	seed := make([]byte, 32)
	copy(seed, "cn engine test seed")

	e := New()
	e.pow = pow
	j := &job{
		id:       "1",
		blob:     blob,
		hashing:  p.hashingBlob(),
		nonceOff: p.nonceOffset,
		seed:     seed,
		height:   1000000,
		diff:     1 << 62, // unreachably hard -> never tries submit_block
		seen:     map[string]struct{}{},
	}
	e.cur = j
	e.history[j.id] = j

	// honest miner: compute the real RandomX result for nonce 0x01020304
	nonce := []byte{1, 2, 3, 4}
	result, err := pow.Hash(seed, withNonce(j.hashing, j.nonceOff, nonce))
	if err != nil {
		t.Fatal(err)
	}

	sess := fakeSession{diff: 0.000001} // accept virtually anything as a share
	submit := func(nonceHex, resultHex string) *types.Share {
		return e.OnSubmit(sess, []interface{}{map[string]interface{}{
			"id": "1", "job_id": "1", "nonce": nonceHex, "result": resultHex,
		}})
	}

	share := submit(hex.EncodeToString(nonce), hex.EncodeToString(result))
	if share.ErrorCode != 0 {
		t.Fatalf("honest share rejected: %s", share.ErrorCode.String())
	}
	if share.Diff <= 0 {
		t.Fatal("share diff must be positive")
	}

	// duplicate nonce
	if submit(hex.EncodeToString(nonce), hex.EncodeToString(result)).ErrorCode != types.ErrDuplicateShare {
		t.Fatal("duplicate nonce must be rejected")
	}

	// forged result for a fresh nonce
	forged := make([]byte, 32)
	if submit("05060708", hex.EncodeToString(forged)).ErrorCode == 0 {
		t.Fatal("forged result must be rejected")
	}

	// unknown job
	bad := e.OnSubmit(sess, []interface{}{map[string]interface{}{
		"id": "1", "job_id": "99", "nonce": "01020304", "result": hex.EncodeToString(result),
	}})
	if bad.ErrorCode != types.ErrJobNotFound {
		t.Fatal("unknown job must be rejected")
	}
}

func TestTargetHexCompact(t *testing.T) {
	// diff 1 -> target 0xffffffffffffffff (LE hex)
	if got := targetHex(1); got != "ffffffffffffffff" {
		t.Fatalf("diff-1 target wrong: %s", got)
	}
	// diff 2^32 -> 2^32 LE = 00000000 01000000
	if got := targetHex(1 << 32); got != "0000000001000000" {
		t.Fatalf("diff-2^32 target wrong: %s", got)
	}
	// round trip sanity: decoding the target back approximates 2^64/diff
	b, _ := hex.DecodeString(targetHex(4000))
	v := binary.LittleEndian.Uint64(b)
	want := new(big.Int).Div(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(4000)).Uint64()
	if v != want {
		t.Fatalf("target(4000) = %d, want %d", v, want)
	}
}
