//go:build kaspa
// +build kaspa

package kaspa

import (
	"encoding/binary"
	"math/big"
	"net"
	"testing"

	"github.com/kaspanet/kaspad/domain/consensus/model/externalapi"
	"github.com/kaspanet/kaspad/domain/consensus/utils/blockheader"
	"github.com/kaspanet/kaspad/domain/consensus/utils/consensushashing"
	"github.com/kaspanet/kaspad/domain/consensus/utils/pow"
	phh "github.com/sencha-dev/powkit/heavyhash"

	"github.com/mining-pool/not-only-mining-pool/types"
)

func zeroHash() *externalapi.DomainHash {
	var b [externalapi.DomainHashSize]byte
	return externalapi.NewDomainHashFromByteArray(&b)
}

func syntheticHeader(t *testing.T, timestamp int64, nonce uint64) externalapi.BlockHeader {
	t.Helper()
	parents := []externalapi.BlockLevelParents{{zeroHash()}}
	return blockheader.NewImmutableBlockHeader(
		1, parents,
		zeroHash(), zeroHash(), zeroHash(),
		timestamp,
		0x1e7fffff, // bits (an easy target)
		nonce,
		12345, // daaScore
		12000, // blueScore
		big.NewInt(1000),
		zeroHash(),
	)
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[len(b)-1-i] = b[i]
	}
	return out
}

// TestHeavyHashCrossImplementation is the key correctness proof: for the same
// header, kaspad's OFFICIAL consensus pow.State and powkit's INDEPENDENT
// heavyhash implementation must compute the identical proof-of-work value.
// Two separate codebases agreeing pins the byte-order and algorithm wiring.
func TestHeavyHashCrossImplementation(t *testing.T) {
	const timestamp int64 = 0x000001848ca87c49
	const nonce uint64 = 0x2f8400000eba167c

	header := syntheticHeader(t, timestamp, nonce)

	// kaspad official path
	state := pow.NewState(header.ToMutable())
	state.Nonce = nonce
	kaspadVal := state.CalculateProofOfWorkValue()

	// recompute prePowHash the way the engine does (zero time+nonce)
	mutable := header.ToMutable()
	mutable.SetTimeInMilliseconds(0)
	mutable.SetNonce(0)
	prePow := consensushashing.HeaderHash(mutable).ByteSlice()

	// powkit independent path
	digest, err := phh.NewKaspa().Compute(prePow, timestamp, nonce)
	if err != nil {
		t.Fatal(err)
	}
	// kaspad's toBig already reverses (little-endian hash -> big.Int); powkit's
	// digest is in that same little-endian order, so SetBytes matches directly.
	powkitVal := new(big.Int).SetBytes(digest)

	if kaspadVal.Cmp(powkitVal) != 0 {
		t.Fatalf("heavyhash mismatch between kaspad and powkit:\n kaspad: %x\n powkit: %x", kaspadVal, powkitVal)
	}
}

func TestLargeJobParamsLayout(t *testing.T) {
	prePow := make([]byte, 32)
	for i := range prePow {
		prePow[i] = byte(i)
	}
	s := largeJobParams(prePow, 0x0102030405060708)
	if len(s) != 80 {
		t.Fatalf("large job must be 80 hex chars, got %d", len(s))
	}
	// first word is big-endian uint64 of prePow[0:8] = 0001020304050607
	if s[:16] != "0001020304050607" {
		t.Fatalf("word0 big-endian wrong: %s", s[:16])
	}
	// last word is the timestamp rendered %016x (bridge convention)
	if s[64:] != "0102030405060708" {
		t.Fatalf("timestamp word wrong: %s", s[64:])
	}
	_ = binary.LittleEndian
}

type fakeSession struct{ diff float64 }

func (f fakeSession) ExtraNonce1() []byte { return nil }
func (f fakeSession) Difficulty() float64 { return f.diff }
func (f fakeSession) WorkerName() string  { return "worker" }
func (f fakeSession) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}
func (f fakeSession) Send(string, []interface{}) error { return nil }

// TestOnSubmitValidationPath exercises submit routing/validation without a node
// (unknown job, bad nonce, duplicate). A network-target hit is not forced (that
// needs submit_block against a live node).
func TestOnSubmitValidationPath(t *testing.T) {
	header := syntheticHeader(t, 0x000001848ca87c49, 0)
	e := New()
	j := &job{
		id:         "1",
		state:      pow.NewState(header.ToMutable()),
		prePowHash: make([]byte, 32),
		seen:       map[string]struct{}{},
	}
	// unreachable target so it never calls SubmitBlock (client is nil)
	j.state.Target.SetInt64(1)
	e.cur = j
	e.history["1"] = j

	sess := fakeSession{diff: 1e50} // require huge diff -> low-diff reject path

	if got := e.OnSubmit(sess, []interface{}{"w", "99", "0x01"}); got.ErrorCode != types.ErrJobNotFound {
		t.Fatalf("unknown job must be rejected, got %v", got.ErrorCode)
	}
	if got := e.OnSubmit(sess, []interface{}{"w", "1", "zznothex"}); got.ErrorCode != types.ErrIncorrectNonceSize {
		t.Fatalf("bad nonce must be rejected, got %v", got.ErrorCode)
	}
	first := e.OnSubmit(sess, []interface{}{"w", "1", "0x0abc"})
	if first.ErrorCode != types.ErrLowDiffShare {
		t.Fatalf("expected low-diff share (huge session diff), got %v", first.ErrorCode)
	}
	if dup := e.OnSubmit(sess, []interface{}{"w", "1", "0x0abc"}); dup.ErrorCode != types.ErrDuplicateShare {
		t.Fatalf("duplicate nonce must be rejected, got %v", dup.ErrorCode)
	}
}
