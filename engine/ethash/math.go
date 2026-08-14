package ethash

import (
	"math/big"

	"github.com/mining-pool/not-only-mining-pool/engine"
)

// TargetFromDifficulty returns the boundary (floor(2^256 / diff)) that a hash
// result must be <= to in order to satisfy the given share difficulty.
func TargetFromDifficulty(diff float64) *big.Int {
	return engine.TargetFromDiff(engine.Pow256, diff)
}

// DifficultyFromResult returns the share difficulty represented by a 256-bit
// hash result: 2^256 / result. A smaller result means a higher difficulty.
func DifficultyFromResult(result *big.Int) float64 {
	return engine.DiffFromValue(engine.Pow256, result)
}

// MeetsTarget reports whether a hash result satisfies (is <=) the target.
func MeetsTarget(result, target *big.Int) bool {
	return result.Cmp(target) <= 0
}
