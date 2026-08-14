package algorithm

import (
	"math/big"
	"strings"
	"sync"

	"github.com/bitgoin/lyra2rev2"
	logging "github.com/ipfs/go-log/v2"
	"github.com/mining-pool/not-only-mining-pool/utils"
	"github.com/samli88/go-x11-hash"
	"github.com/samli88/go-x11-hash/groestl"
	"github.com/sencha-dev/powkit/verthash"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/crypto/sha3"
)

var log = logging.Logger("algorithm")

// difficulty = MAX_TARGET / current_target.
var (
	MaxTargetTruncated, _ = new(big.Int).SetString("00000000FFFF0000000000000000000000000000000000000000000000000000", 16)
	MaxTarget, _          = new(big.Int).SetString("00000000FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", 16)
)

// HashFunc turns a serialized block header (or coinbase) into its PoW hash.
type HashFunc func([]byte) []byte

// registry maps a lowercased algorithm name to its header PoW hash function.
//
// Every coin that this pool supports shares the standard 80-byte Bitcoin block
// header (see jobs.Job.SerializeHeader); the ONLY thing that differs between
// coins is the function used to hash that header. Register a new function here
// (or via RegisterHash from your own package/cgo binding) and the coin becomes
// mineable without touching the job engine.
var registry = map[string]HashFunc{
	"sha256":    Sha256Hash,
	"sha256d":   DoubleSha256Hash,
	"scrypt":    ScryptHash,
	"x11":       X11Hash,
	"keccak":    KeccakHash,
	"groestl":   GroestlHash,
	"lyra2rev2": Lyra2Rev2Hash,
	"verthash":  VerthashHash,
}

// defaultMultipliers holds the conventional diff-1 shift (2^multiplier applied
// to the truncated max target) for each algorithm. It is only used to fill in a
// sane value when the config omits (or zeroes) "algorithm.multiplier".
//
// scrypt/x11/neoscrypt style algorithms are ~2^16 times "heavier" per hash than
// a raw sha256d, so their share difficulty must be scaled to keep the reported
// hashrate and share cadence comparable across algorithms.
var defaultMultipliers = map[string]uint{
	"sha256":    0,
	"sha256d":   0,
	"scrypt":    16,
	"x11":       30,
	"keccak":    8,
	"groestl":   8,
	"lyra2rev2": 8,
	"verthash":  8,
}

// RegisterHash lets external code (e.g. a cgo binding for neoscrypt, lyra2rev2,
// x16r, groestl, ...) plug a new algorithm into the pool at init time without
// modifying this package:
//
//	func init() { algorithm.RegisterHash("neoscrypt", 16, neoScryptHash) }
//
// name is matched case-insensitively against config's "algorithm.name".
func RegisterHash(name string, defaultMultiplier uint, fn HashFunc) {
	key := strings.ToLower(name)
	registry[key] = fn
	defaultMultipliers[key] = defaultMultiplier
}

// IsSupported reports whether the named algorithm has a registered hash function.
func IsSupported(hashName string) bool {
	_, ok := registry[strings.ToLower(hashName)]
	return ok
}

// SupportedAlgorithms returns the names of every registered algorithm.
func SupportedAlgorithms() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// DefaultMultiplier returns the conventional diff-1 shift for an algorithm, or 0
// when the algorithm is unknown / needs no scaling.
func DefaultMultiplier(hashName string) uint {
	return defaultMultipliers[strings.ToLower(hashName)]
}

func GetHashFunc(hashName string) HashFunc {
	if fn, ok := registry[strings.ToLower(hashName)]; ok {
		return fn
	}

	log.Panic(hashName, " is not officially supported yet, but you can easily add it with a pure-go or cgo binding via algorithm.RegisterHash. Supported: ", strings.Join(SupportedAlgorithms(), ", "))
	return nil
}

// ScryptHash is the algorithm which litecoin/dogecoin use as their PoW.
func ScryptHash(data []byte) []byte {
	b, _ := scrypt.Key(data, data, 1024, 1, 1, 32)

	return b
}

// X11Hash is the algorithm which dash uses as its PoW.
func X11Hash(data []byte) []byte {
	dst := make([]byte, 32)
	x11.New().Hash(data, dst)
	return dst
}

// Sha256Hash is a single round of SHA-256.
func Sha256Hash(b []byte) []byte {
	return utils.Sha256(b)
}

// DoubleSha256Hash is the algorithm which bitcoin uses as its PoW.
func DoubleSha256Hash(b []byte) []byte {
	return utils.Sha256d(b)
}

// KeccakHash is the (legacy, pre-NIST) Keccak-256 used by keccak coins such as
// Maxcoin / Smartcash-style chains.
func KeccakHash(b []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(b)
	return h.Sum(nil)
}

// GroestlHash is the algorithm which groestlcoin uses as its PoW: two rounds of
// Groestl-512 truncated to 256 bits. NOTE: GRS identifies blocks by a SINGLE
// sha256 of the header — set `"blockHasher": "sha256"` in the config.
func GroestlHash(b []byte) []byte {
	round1 := make([]byte, 64)
	d := groestl.New()
	_, _ = d.Write(b)
	_ = d.Close(round1, 0, 0)

	round2 := make([]byte, 64)
	d = groestl.New()
	_, _ = d.Write(round1)
	_ = d.Close(round2, 0, 0)

	return round2[:32]
}

// Lyra2Rev2Hash is the algorithm which monacoin (and pre-fork vertcoin) uses as
// its PoW: blake256 -> keccak256 -> cubehash -> lyra2 -> skein -> cubehash -> bmw.
func Lyra2Rev2Hash(b []byte) []byte {
	sum, err := lyra2rev2.Sum(b)
	if err != nil {
		log.Panic("lyra2rev2 failed: ", err)
	}
	return sum
}

// verthashClient is created lazily: powkit generates (or memory-maps) the
// ~1.2GB verthash.dat under ~/.powcache on first use, which takes a long time
// on first run — do NOT pay that cost unless verthash is actually mined.
var (
	verthashOnce   sync.Once
	verthashClient *verthash.Client
)

func initVerthash() {
	verthashOnce.Do(func() {
		log.Warn("initializing verthash: the ~1.2GB verthash.dat will be generated under ~/.powcache if missing — first run can take a long time")
		c, err := verthash.New()
		if err != nil {
			log.Panic("verthash init failed: ", err)
		}
		verthashClient = c
	})
}

// VerthashHash is the algorithm which vertcoin uses as its PoW. It requires the
// 1.2GB verthash.dat (auto-generated on first use; see initVerthash).
func VerthashHash(b []byte) []byte {
	initVerthash()
	return verthashClient.Compute(b)
}

// warmups lists algorithms that need expensive one-off initialization. Warmup
// lets the pool pay that cost at startup instead of on the first submitted share.
var warmups = map[string]func(){
	"verthash": initVerthash,
}

// Warmup performs any expensive one-off initialization for the named algorithm
// (e.g. generating/loading verthash.dat). Safe to call for any algorithm.
func Warmup(hashName string) {
	if fn, ok := warmups[strings.ToLower(hashName)]; ok {
		fn()
	}
}
