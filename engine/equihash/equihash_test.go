package equihash

import (
	"encoding/hex"
	"math/big"
	"net"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/engine"

	"github.com/mining-pool/not-only-mining-pool/types"
	"github.com/mining-pool/not-only-mining-pool/utils"
)

// Real Flux (ZelHash, Equihash 125,4) block header + solution from the powkit
// test suite: a genuine 140-byte header whose last 32 bytes are the nonce.
const (
	fluxHeaderHex = "0400000038" + "9f266cdabe6a7c4e108814779ba13aa089431fba5149dfde20887e09000000" +
		"24dd82bd2aedfdd633c49c12ad8144f590a2f23dd03d5f993a70dbc68a921dc2" +
		"d9988e7adc9c6da15d12246eeb18623d1f8039f5ee6bcae29261e383248d7522" +
		"27ca9862b2fb1a1d2000c19a000000000000000000000000000000000000000000000000ba098e10"
	fluxSolutionHex = "47729e612ed3c5c2dec26730db486d5e7d78d13b0735b2cddb54619f6f31a646" +
		"178fb35f3f84636c5490edd4f6f9a5010f2aaac8"
)

func fluxVector(t *testing.T) (header, sol []byte) {
	t.Helper()
	header, err := hex.DecodeString(fluxHeaderHex)
	if err != nil || len(header) != 140 {
		t.Fatalf("bad flux header vector: len=%d err=%v", len(header), err)
	}
	sol, err = hex.DecodeString(fluxSolutionHex)
	if err != nil || len(sol) != 52 {
		t.Fatalf("bad flux solution vector: len=%d err=%v", len(sol), err)
	}
	return header, sol
}

func TestVariants(t *testing.T) {
	z, err := variantFor("equihash")
	if err != nil || z.solBytes != 1344 {
		t.Fatalf("zcash 200,9 solution must be 1344 bytes, got %d (%v)", z.solBytes, err)
	}
	f, err := variantFor("zelhash")
	if err != nil || f.solBytes != 52 {
		t.Fatalf("flux 125,4 solution must be 52 bytes, got %d (%v)", f.solBytes, err)
	}
	if _, err := variantFor("nonsense"); err == nil {
		t.Fatal("unknown variant must error")
	}
}

func TestMerkleRoot(t *testing.T) {
	tx := []byte{1, 2, 3}
	if hex.EncodeToString(merkleRoot([][]byte{tx})) != hex.EncodeToString(utils.Sha256d(tx)) {
		t.Fatal("single-tx merkle root must be its own hash")
	}
	// two txs: sha256d(h1 || h2)
	tx2 := []byte{4, 5, 6}
	want := utils.Sha256d(append(utils.Sha256d(tx), utils.Sha256d(tx2)...))
	if hex.EncodeToString(merkleRoot([][]byte{tx, tx2})) != hex.EncodeToString(want) {
		t.Fatal("two-tx merkle root wrong")
	}
}

func TestTrimSolutionPrefix(t *testing.T) {
	raw := make([]byte, 52)
	if trimSolutionPrefix(append([]byte{0x34}, raw...), 52) == nil {
		t.Fatal("1-byte compact prefix must be stripped")
	}
	big := make([]byte, 1344)
	if trimSolutionPrefix(append([]byte{0xfd, 0x40, 0x05}, big...), 1344) == nil {
		t.Fatal("3-byte compact prefix must be stripped")
	}
	if trimSolutionPrefix(raw[:51], 52) != nil {
		t.Fatal("wrong-length solution must be rejected")
	}
}

type fakeSession struct {
	en1  []byte
	diff float64
}

func (f fakeSession) ExtraNonce1() []byte { return f.en1 }
func (f fakeSession) Difficulty() float64 { return f.diff }
func (f fakeSession) WorkerName() string  { return "tester" }
func (f fakeSession) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}
func (f fakeSession) Send(string, []interface{}) error { return nil }

// engineWithFluxJob builds an engine + job around the real Flux vector so
// OnSubmit runs the genuine verification path.
func engineWithFluxJob(t *testing.T) (*Engine, fakeSession, []interface{}) {
	header, sol := fluxVector(t)

	v, _ := variantFor("zelhash")
	e := New()
	e.v = v

	j := &job{
		id:        "1",
		prefix108: header[:108],
		ntimeLE:   hex.EncodeToString(header[100:104]),
		target:    big.NewInt(1), // unreachably hard -> never tries submitblock
		seen:      map[string]struct{}{},
	}
	e.cur = j
	e.history[j.id] = j

	sess := fakeSession{en1: header[108:112], diff: 1e-40}
	params := []interface{}{
		"tester", "1",
		j.ntimeLE,
		hex.EncodeToString(header[112:140]),
		hex.EncodeToString(sol),
	}
	return e, sess, params
}

func TestOnSubmitFluxVector(t *testing.T) {
	e, sess, params := engineWithFluxJob(t)

	share := e.OnSubmit(sess, params)
	if share.ErrorCode != 0 {
		t.Fatalf("known-good flux vector rejected: %v (%s)", int(share.ErrorCode), share.ErrorCode.String())
	}

	// duplicate rejected
	if e.OnSubmit(sess, params).ErrorCode != types.ErrDuplicateShare {
		t.Fatal("duplicate submit must be rejected")
	}
}

func TestOnSubmitPrefixedSolutionAndRejections(t *testing.T) {
	e, sess, params := engineWithFluxJob(t)

	// with 1-byte compact prefix (0x34 = 52) — must also be accepted
	prefixed := append([]interface{}{}, params...)
	prefixed[4] = "34" + params[4].(string)
	if share := e.OnSubmit(sess, prefixed); share.ErrorCode != 0 {
		t.Fatalf("compact-prefixed solution rejected: %s", share.ErrorCode.String())
	}

	// wrong ntime
	badTime := append([]interface{}{}, params...)
	badTime[2] = "00000000"
	if e.OnSubmit(sess, badTime).ErrorCode != types.ErrNTimeOutOfRange {
		t.Fatal("wrong ntime must be rejected")
	}

	// corrupted solution
	badSol := append([]interface{}{}, params...)
	badSol[3] = "00" + params[3].(string)[2:] // different nonce2 -> not a dup
	if e.OnSubmit(sess, badSol).ErrorCode == 0 {
		t.Fatal("solution for a different nonce must be rejected")
	}

	// wrong job
	badJob := append([]interface{}{}, params...)
	badJob[1] = "77"
	if e.OnSubmit(sess, badJob).ErrorCode != types.ErrJobNotFound {
		t.Fatal("unknown job must be rejected")
	}
}

func TestShareTargetMath(t *testing.T) {
	if engine.TargetFromDiff(diff1, 1).Cmp(diff1) != 0 {
		t.Fatal("engine.TargetFromDiff(diff1, 1) must equal diff1")
	}
	if engine.TargetFromDiff(diff1, 16).Cmp(engine.TargetFromDiff(diff1, 1)) >= 0 {
		t.Fatal("higher diff must shrink target")
	}
	if got := engine.TargetHex(big.NewInt(0x0f)); len(got) != 64 {
		t.Fatalf("targetHex must be 64 chars, got %d", len(got))
	}
	if d := engine.DiffFromValue(diff1, new(big.Int).Set(diff1)); d < 0.999 || d > 1.001 {
		t.Fatalf("diffFromHash(diff1) should be ~1, got %v", d)
	}
}
