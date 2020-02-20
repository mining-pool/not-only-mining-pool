package algorithm

import (
	"golang.org/x/crypto/scrypt"
	"log"
	"math/big"
)

const (
	Multiplier = 1 << 16 // Math.pow(2, 16)
)

const Name = "scrypt"

// difficulty = MAX_TARGET / current_target.
var (
	MaxTargetTruncated, _ = new(big.Int).SetString("00000000FFFF0000000000000000000000000000000000000000000000000000", 16)
	MaxTarget, _          = new(big.Int).SetString("00000000FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", 16)
)

func Hash(data []byte) []byte {
	return ScryptHash(data)
}

func ScryptHash(data []byte) []byte {
	b, err := scrypt.Key(data, data, 1024, 1, 1, 32)
	if err != nil {
		log.Panic(err)
	}

	return b
}
