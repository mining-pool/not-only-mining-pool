package engine

import (
	"math"
	"math/big"
	"testing"
)

func TestTargetFromDiffRoundTrip(t *testing.T) {
	for _, diff := range []float64{1, 2, 1000, 1e6, 4.2e9} {
		got := DiffFromValue(Pow256, TargetFromDiff(Pow256, diff))
		if d := got / diff; d < 0.999 || d > 1.001 {
			t.Fatalf("roundtrip diff=%v -> %v", diff, got)
		}
	}
	// diff <= 0 must not panic and yields the base (everything passes)
	if TargetFromDiff(Pow256, 0).Cmp(Pow256) != 0 {
		t.Fatal("diff<=0 should yield the base target")
	}
	if DiffFromValue(Pow256, big.NewInt(0)) != 0 {
		t.Fatal("zero value -> 0 diff")
	}
}

func TestTargetHex(t *testing.T) {
	if got := TargetHex(big.NewInt(0xff)); len(got) != 64 || got[63] != 'f' || got[62] != 'f' || got[61] != '0' {
		t.Fatalf("TargetHex padding wrong: %q", got)
	}
	// overflow (> 32 bytes) clamps to all-f
	big33 := new(big.Int).Lsh(big.NewInt(1), 264)
	if got := TargetHex(big33); got != "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" {
		t.Fatalf("overflow should clamp: %q", got)
	}
}

func TestHashesPerDiff(t *testing.T) {
	if got := HashesPerDiff(Pow256); got != 1 {
		t.Fatalf("2^256 base = %v hashes per diff, want 1", got)
	}
	// Bitcoin difficulty 1: 0x00000000FFFF << 208 => 2^48 / 0xFFFF ≈ 2^32.
	btc, _ := new(big.Int).SetString("00000000FFFF0000000000000000000000000000000000000000000000000000", 16)
	if got := HashesPerDiff(btc); math.Abs(got-4295032833.000015) > 1e-3 {
		t.Fatalf("bitcoin diff1 = %v hashes", got)
	}
}
