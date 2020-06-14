package algorithm

import (
	logging "github.com/ipfs/go-log"
	"github.com/mining-pool/go-pool-server/utils"
	"github.com/samli88/go-x11-hash"
	"golang.org/x/crypto/scrypt"
	"math/big"
	"strings"
)

var log = logging.Logger("algorithm")

// difficulty = MAX_TARGET / current_target.
var (
	MaxTargetTruncated, _ = new(big.Int).SetString("00000000FFFF0000000000000000000000000000000000000000000000000000", 16)
	MaxTarget, _          = new(big.Int).SetString("00000000FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", 16)
)

func GetHashFunc(hashName string) func([]byte) []byte {
	switch strings.ToLower(hashName) {
	case "scrypt":
		return ScryptHash
	case "x11":
		return X11Hash
	case "sha256d":
		return DoubleSha256Hash
	default:
		log.Panic(hashName, " is not officially supported yet, but you can easily add it with cgo binding by yourself")
		return nil
	}
}

// ScryptHash is the algorithm which litecoin uses as its PoW mining algorithm
func ScryptHash(data []byte) []byte {
	b, _ := scrypt.Key(data, data, 1024, 1, 1, 32)

	return b
}

// X11Hash is the algorithm which dash uses as its PoW mining algorithm
func X11Hash(data []byte) []byte {
	dst := make([]byte, 32)
	x11.New().Hash(data, dst)
	return dst
}

// DoubleSha256Hash is the algorithm which litecoin uses as its PoW mining algorithm
func DoubleSha256Hash(b []byte) []byte {
	return utils.Sha256d(b)
}
