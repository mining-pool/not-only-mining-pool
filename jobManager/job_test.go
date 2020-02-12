package jobManager

import (
	"encoding/hex"
	"fmt"
	"github.com/node-standalone-pool/go-pool-server/algorithm"
	"github.com/node-standalone-pool/go-pool-server/utils"
	"math/big"
	"testing"
)

func TestNewBlockTemplate(t *testing.T) {
	// 1e06109b bits
	// 000006109b000000000000000000000000000000000000000000000000000000 target
	// 0.0006395185894153062 diff

	target, _ := hex.DecodeString("000006109b000000000000000000000000000000000000000000000000000000")
	bigTarget := new(big.Float).SetInt(new(big.Int).SetBytes(target))
	diff := big.NewFloat(0.0006395185894153062)
	fmt.Println(0.0006395185894153062)
	maxTarget := new(big.Float).SetInt(algorithm.MaxTarget)
	fmt.Println(algorithm.MaxTarget)
	fmt.Println(maxTarget)
	fmt.Println(new(big.Float).SetInt(new(big.Int).SetBytes(target)))
	fmt.Println(new(big.Float).Mul(bigTarget, diff))
	fmt.Println(utils.BigIntFromBitsHex("1e06109b"))

	fmt.Println(algorithm.MaxTarget)
}

func TestJob_SerializeBlock(t *testing.T) {
}
