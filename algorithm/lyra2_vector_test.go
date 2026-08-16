package algorithm

import (
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/utils"
)

// TestLyra2Rev2AgainstRealMonacoinBlock verifies the pool's Lyra2REv2 against a
// real accepted Monacoin block (2500000): its PoW hash MUST be <= the block
// target. If not, the library disagrees with monacoind.
func TestLyra2Rev2AgainstRealMonacoinBlock(t *testing.T) {
	const (
		version       = uint32(536870912)
		prevDisplay   = "20f986555ce18dc5c5de9a69faf6d646026d7ba8da30350e2a93ae1912bbe690"
		merkleDisplay = "4dabf61d902f3ca4b22f227287b693914f6287957485b39d68379fdd9d01a241"
		ntime         = uint32(1637217290)
		bits          = uint32(0x1a067960)
		nonce         = uint32(3183243202)
	)
	prev, _ := hex.DecodeString(prevDisplay)
	merkle, _ := hex.DecodeString(merkleDisplay)

	hdr := make([]byte, 0, 80)
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], version)
	hdr = append(hdr, b[:]...)
	hdr = append(hdr, utils.ReverseBytes(prev)...)
	hdr = append(hdr, utils.ReverseBytes(merkle)...)
	binary.LittleEndian.PutUint32(b[:], ntime)
	hdr = append(hdr, b[:]...)
	binary.LittleEndian.PutUint32(b[:], bits)
	hdr = append(hdr, b[:]...)
	binary.LittleEndian.PutUint32(b[:], nonce)
	hdr = append(hdr, b[:]...)
	if len(hdr) != 80 {
		t.Fatalf("header len %d", len(hdr))
	}

	pow := Lyra2Rev2Hash(hdr)
	powBig := new(big.Int).SetBytes(utils.ReverseBytes(pow))

	exp := bits >> 24
	target := new(big.Int).Lsh(big.NewInt(int64(bits&0xffffff)), uint(8*(exp-3)))

	t.Logf("pow(reversed) = %x", utils.ReverseBytes(pow))
	t.Logf("pow(as-is)    = %x", pow)
	t.Logf("target        = %064x", target)
	if powBig.Cmp(target) > 0 {
		t.Errorf("POW > target — bitgoin/lyra2rev2 does NOT match monacoind (a real accepted block fails PoW)")
	}
}
