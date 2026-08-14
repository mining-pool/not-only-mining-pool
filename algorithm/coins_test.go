package algorithm

import (
	"encoding/hex"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/utils"
)

// blockHash reproduces exactly what jobs.JobManager does when a share reaches
// network difficulty: the coin's block identifier is reverse(hash(header)),
// where hash is sha256d for the sha256d-block-hasher family, otherwise the
// coin's own PoW algorithm.
func blockHash(header []byte, algo string, sha256dBlockHasher bool) string {
	if sha256dBlockHasher {
		return hex.EncodeToString(utils.ReverseBytes(utils.Sha256d(header)))
	}
	return hex.EncodeToString(utils.ReverseBytes(GetHashFunc(algo)(header)))
}

// The Bitcoin genesis block is the canonical known-answer vector for the whole
// sha256d family (BTC/BCH/BSV/DGB-sha256d/NMC/PPC all serialize the identical
// 80-byte header and identify blocks the same way).
//
//	version    01000000
//	prevhash   00..00 (32B)
//	merkleroot 3ba3edfd7a7b12b27ac72c3e67768f617fc81bc3888a51323a9fb8aa4b1e5e4a
//	time       29ab5f49 (1231006505)
//	bits       ffff001d (0x1d00ffff)
//	nonce      1dac2b7c (2083236893)
const bitcoinGenesisHeaderHex = "010000000000000000000000000000000000000000000000000000000000000000000000" +
	"3ba3edfd7a7b12b27ac72c3e67768f617fc81bc3888a51323a9fb8aa4b1e5e4a29ab5f49ffff001d1dac2b7c"

const bitcoinGenesisHash = "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f"

func TestKnownAnswer_SHA256D_BitcoinGenesis(t *testing.T) {
	header, err := hex.DecodeString(bitcoinGenesisHeaderHex)
	if err != nil {
		t.Fatal(err)
	}
	if len(header) != 80 {
		t.Fatalf("header must be 80 bytes, got %d", len(header))
	}

	got := blockHash(header, "sha256d", true)
	if got != bitcoinGenesisHash {
		t.Fatalf("sha256d block hash mismatch\n got: %s\nwant: %s", got, bitcoinGenesisHash)
	}
}

// TestTop20Coverage documents, and asserts, exactly which of the top-20 PoW
// mining coins this pool can serve. It is the single source of truth for the
// adaptation status: A = ready, B = needs a cgo hash binding, C = incompatible
// engine (different header / mining model, out of scope for a GBT pool).
func TestTop20Coverage(t *testing.T) {
	type coin struct {
		symbol string
		algo   string
		class  byte // 'A' ready, 'B' needs binding, 'C' incompatible
	}

	top20 := []coin{
		// A — ready out of the box (algorithm registered natively)
		{"BTC", "sha256d", 'A'},
		{"BCH", "sha256d", 'A'},
		{"BSV", "sha256d", 'A'},
		{"LTC", "scrypt", 'A'},
		{"DOGE", "scrypt", 'A'},
		{"DASH", "x11", 'A'},
		{"DGB", "sha256d", 'A'},
		{"NMC", "sha256d", 'A'},
		{"PPC", "sha256d", 'A'},
		{"MAX", "keccak", 'A'},
		{"GRS", "groestl", 'A'},    // pure-Go via samli88/go-x11-hash groestl512
		{"VTC", "verthash", 'A'},   // pure-Go via powkit (needs 1.2GB verthash.dat)
		{"MONA", "lyra2rev2", 'A'}, // pure-Go via bitgoin/lyra2rev2
		// B — GBT-compatible header hash, registered under a cgo build tag
		{"FTC", "neoscrypt", 'B'}, // -tags neoscrypt
		// C — incompatible with the GBT header-hash flow. ETC and RVN are served
		// through pluggable engines instead (engine/ethash, engine/kawpow); the
		// assertion below only checks they are absent from the HEADER-HASH
		// registry, which must stay true.
		{"ETC", "etchash", 'C'},  // engine: ethash (build with -tags ethash)
		{"XMR", "randomx", 'C'},  // engine: cryptonote (build with -tags randomx)
		{"ZEC", "equihash", 'C'}, // engine: equihash (always built in)
		{"RVN", "kawpow", 'C'},     // engine: kawpow (always built in)
		{"KAS", "kheavyhash", 'C'}, // engine: kaspa (build with -tags kaspa)
		{"ERG", "autolykos2", 'C'}, // engine: ergo (always built in)
	}

	if len(top20) != 20 {
		t.Fatalf("expected 20 coins, got %d", len(top20))
	}

	for _, c := range top20 {
		switch c.class {
		case 'A':
			if !IsSupported(c.algo) {
				t.Errorf("%s: class A but algorithm %q is NOT registered", c.symbol, c.algo)
			}
		case 'B':
			// Intentionally not registered here: shipped as a config template,
			// enabled by the operator via a cgo binding + RegisterHash.
			if IsSupported(c.algo) {
				t.Logf("%s: algorithm %q is now registered — promote it to class A in the docs", c.symbol, c.algo)
			}
		case 'C':
			if IsSupported(c.algo) {
				t.Errorf("%s: algorithm %q marked incompatible but is registered; a GBT pool cannot actually serve %s", c.symbol, c.algo, c.symbol)
			}
		}
	}

	var a, b, cc int
	for _, c := range top20 {
		switch c.class {
		case 'A':
			a++
		case 'B':
			b++
		case 'C':
			cc++
		}
	}
	t.Logf("top-20 adaptation: %d ready (A), %d need-binding (B), %d incompatible (C)", a, b, cc)
}
