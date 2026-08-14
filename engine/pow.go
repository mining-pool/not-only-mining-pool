package engine

import "math/big"

// Pow256 is 2^256, the size of most PoW hash spaces.
var Pow256 = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil)

// TargetFromDiff returns floor(base / diff): the boundary a hash value must be
// <= to satisfy the given share difficulty. base is the difficulty-1 target
// (usually Pow256, or a coin's packed diff-1). diff <= 0 is treated as 1.
func TargetFromDiff(base *big.Int, diff float64) *big.Int {
	if diff <= 0 {
		diff = 1
	}
	t, _ := new(big.Float).Quo(new(big.Float).SetInt(base), big.NewFloat(diff)).Int(nil)
	return t
}

// DiffFromValue returns base / value: the difficulty a hash value represents.
func DiffFromValue(base, value *big.Int) float64 {
	if value.Sign() <= 0 {
		return 0
	}
	d, _ := new(big.Float).Quo(new(big.Float).SetInt(base), new(big.Float).SetInt(value)).Float64()
	return d
}

// TargetHex renders a target as a 32-byte zero-padded (big-endian) hex string.
func TargetHex(t *big.Int) string {
	s := t.Text(16)
	if len(s) > 64 {
		return "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	}
	return "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"[:64-len(s)] + s
}
