package algorithm

import (
	"encoding/hex"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/utils"
)

func TestHash(t *testing.T) {
	t.Log(MaxTargetTruncated)
}

func TestScryptHash(t *testing.T) {
	headerHex := "01000000f615f7ce3b4fc6b8f61e8f89aedb1d0852507650533a9e3b10b9bbcc30639f279fcaa86746e1ef52d3edb3c4ad8259920d509bd073605c9bf1d59983752a6b06b817bb4ea78e011d012d59d4"
	headerBytes, err := hex.DecodeString(headerHex)
	if err != nil {
		t.Log(err)
	}
	result := hex.EncodeToString(utils.ReverseBytes(GetHashFunc("scrypt")(headerBytes)))
	if result != "0000000110c8357966576df46f3b802ca897deb7ad18b12f1c24ecff6386ebd9" {
		t.Log(result)
		t.Fail()
	}
}

func TestKeccakHash(t *testing.T) {
	// legacy Keccak-256 of empty input (same as Ethereum's keccak256)
	if hex.EncodeToString(KeccakHash([]byte(""))) != "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470" {
		t.Log(hex.EncodeToString(GetHashFunc("keccak")([]byte(""))))
		t.Fail()
	}
}

func TestLyra2Rev2Hash(t *testing.T) {
	// known-answer vector from the bitgoin/lyra2rev2 test suite:
	// an 80-byte header-sized buffer beginning with "test", zero-padded
	data := make([]byte, 80)
	copy(data, []byte("test"))
	if hex.EncodeToString(Lyra2Rev2Hash(data)) != "5f21d7763b1ae8fc87db7dc993ddc50468765729411ba6b24906de15851a4abf" {
		t.Log(hex.EncodeToString(GetHashFunc("lyra2rev2")(data)))
		t.Fail()
	}
}

func TestGroestlHash(t *testing.T) {
	// structural checks: 32-byte digest, deterministic, input-sensitive.
	// NOTE: no public double-groestl-512/trim256 vector is embedded here — GRS
	// adaptation must be regression-tested against a real GRS regtest block
	// (scripts/e2e.sh) before mainnet use.
	data := make([]byte, 80)
	copy(data, []byte("test"))

	h1 := GroestlHash(data)
	h2 := GroestlHash(data)
	if len(h1) != 32 {
		t.Fatalf("groestl digest must be 32 bytes, got %d", len(h1))
	}
	if hex.EncodeToString(h1) != hex.EncodeToString(h2) {
		t.Fatal("groestl must be deterministic")
	}
	data[0] ^= 1
	if hex.EncodeToString(GroestlHash(data)) == hex.EncodeToString(h1) {
		t.Fatal("groestl must be input-sensitive")
	}
}

func TestRegistry(t *testing.T) {
	// NOTE: verthash is registered but deliberately not invoked here — its first
	// call generates a ~1.2GB datafile (see initVerthash / Warmup).
	for _, name := range []string{"sha256", "sha256d", "scrypt", "x11", "keccak", "groestl", "lyra2rev2", "verthash"} {
		if !IsSupported(name) {
			t.Fatalf("expected %s to be supported", name)
		}
	}

	if IsSupported("no-such-algo") {
		t.Fatal("unexpected support for no-such-algo")
	}

	RegisterHash("dummy", 7, func(b []byte) []byte { return b })
	if !IsSupported("DUMMY") { // case-insensitive
		t.Fatal("RegisterHash did not register case-insensitively")
	}
	if DefaultMultiplier("dummy") != 7 {
		t.Fatal("DefaultMultiplier mismatch after RegisterHash")
	}
}

func TestX11Hash(t *testing.T) {
	if hex.EncodeToString(X11Hash([]byte("The great experiment continues."))) != "4da3b7c5ff698c6546564ebc72204f31885cd87b75b2b3ca5a93b5d75db85b8c" {
		t.Log(hex.EncodeToString(GetHashFunc("x11")([]byte("The great experiment continues."))))
		t.Fail()
	}

	// Test Dash Tx
	raw, _ := hex.DecodeString("0200000001ac7d18f0103f17c44b5b2b1352617735cc3a3a52381a28e923dffa4ac78e1560000000006b483045022100c56b739271efc559d63b04a01c15fddf7a74008b9afbd432c6260c24bde3b0cf02206ce80233e5af953f7e6f4b55427afa86aac6cbf3047c3cf90fcc248c8d3338f9012103e544bf462f31edad02b3d8134f60d20d7180208df68b0d95f8e0cacee880bc93ffffffff013d6c6d02000000001976a91404ed220f5b5bfd1c61becf0d76e21773ed204ac188ac00000000")
	hash := hex.EncodeToString(DoubleSha256Hash(raw))

	if hash != hex.EncodeToString(utils.Uint256BytesFromHash("498a7a14586da86d98a26ee00aecb7f8fb61a6160453186c88108e4873beaaff")) {
		t.Log(hash)
		t.Fail()
	}
}
