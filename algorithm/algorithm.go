package algorithm

import (
	"github.com/mining-pool/go-pool-server/utils"
	"github.com/samli88/go-x11-hash"
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
	return X11Hash(data)
}

// ScryptHash is the algorithm which litecoin uses as its PoW mining algorithm
func ScryptHash(data []byte) []byte {
	b, err := scrypt.Key(data, data, 1024, 1, 1, 32)
	if err != nil {
		log.Panic(err)
	}

	return b
}

func X11Hash(data []byte) []byte {
	dst := make([]byte, 32)
	x11.New().Hash(dst, data)
	return dst
}

// DoubleSha256Hash is the algorithm which litecoin uses as its PoW mining algorithm
func DoubleSha256Hash(b []byte) []byte {
	return utils.Sha256d(b)
}
