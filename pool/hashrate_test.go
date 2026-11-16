package pool

import (
	"math"
	"testing"
)

func TestGBTHashesPerDiffFollowsTheMultiplier(t *testing.T) {
	// Difficulty 1 is ~2^32 hashes for sha256d and 2^32/2^16 for scrypt.
	if got := gbtHashesPerDiff(0); math.Abs(got-4295032833.000015) > 1e-3 {
		t.Fatalf("sha256d: %v", got)
	}
	if got := gbtHashesPerDiff(16); math.Abs(got-65537.0000152590) > 1e-6 {
		t.Fatalf("scrypt: %v", got)
	}
}
