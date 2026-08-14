package cryptonote

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// treeHashVectors were generated from Monero's REFERENCE C implementation
// (src/crypto/tree-hash.c + keccak.c, fetched from monero-project/monero
// master and compiled locally): leaf i = keccak256([byte(i)]), root over the
// first `count` leaves. This pins our Go port to consensus behaviour,
// including the unusual partial-leaf-compression step.
var treeHashVectors = []string{
	1:  "bc36789e7a1e281436464229828f817d6612f7b477d66591ff96a9e064bcc98a",
	2:  "57d772147cdf27f5f67d679f0f3a513f8b87622ce598a3cf0b048ab178ddfc6e",
	3:  "31ea648480acca9d46c5cfd2fd5ecf576ce7a797bdd582869c38deeacf6d17d4",
	4:  "dd5115b5dcca3db0bffa31064a0d21f21362cd02e1263e47d69e38bbeec1d359",
	5:  "3b85b9b4e7171846e3dd41d242f99cdc136467ff276a272d5d8f960b2c447d67",
	6:  "339caf14b48992a6c4f2f7fcdb491952fb108febcab38667df0828be8f3651a7",
	7:  "6db3924fa166ddef0003d700474beb10c7cd9cc90b882af3b1bbb98aeb557a5f",
	8:  "791521f02a712f28265f5200914f9772b133bc2692260f8c8f426e176b1713ed",
	9:  "6a31a9bc64f694b411012bf9293fbf312a418c49565fcee0b0125c5c768c77be",
	10: "83f3ea05f97c2b13dffe699838822b881a9e8aea9be662f3e4f4fb7cd48165be",
	11: "faf740f2b51e0a95c83d3adaef7c2dd3295cd6eddf13279a4f0bd05413ba1a17",
	12: "03025f19f361844d28742171526e5d5206f8cdaf85cf34a909893fb6b7a5e219",
	13: "bc35ba2d541045a10ba7e7fe75b3a80505e1455d88552cafdf50dbc99237310a",
	14: "e3e7a4982f70025565d66872ca34f915004afea1c7ba9894746e0287e3e9a997",
	15: "7c2dec15c289f33ca52a47022f42b73ebca34b0fb23394a78ced4be0ea606689",
	16: "697bead87db24f50e7e851c6d364c121829786ebd8b1bea2811fa47a6a3716d8",
}

func TestTreeHashAgainstMoneroReference(t *testing.T) {
	leaves := make([][]byte, 16)
	for i := range leaves {
		leaves[i] = keccak256([]byte{byte(i)})
	}
	for count := 1; count <= 16; count++ {
		got := hex.EncodeToString(treeHash(leaves[:count]))
		if got != treeHashVectors[count] {
			t.Errorf("tree_hash(%d leaves) = %s, want %s", count, got, treeHashVectors[count])
		}
	}
}

func TestVarintRoundTrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 0x7f, 0x80, 0x3fff, 0x4000, 1<<32 - 1, 1 << 40} {
		b := appendVarint(nil, v)
		got, next, err := readVarint(b, 0)
		if err != nil || got != v || next != len(b) {
			t.Fatalf("varint roundtrip %d: got %d next %d err %v", v, got, next, err)
		}
	}
}

// buildSyntheticBlob assembles a minimal but structurally correct block blob:
// v14 header + a v2 miner tx (1 gen input, 1 tagged-key output, extra with a
// reserved slot) + two fake tx hashes. Returns the blob and the extra offset.
func buildSyntheticBlob(t *testing.T, txHashes [][]byte) []byte {
	t.Helper()
	blob := appendVarint(nil, 14)         // major
	blob = appendVarint(blob, 14)         // minor
	blob = appendVarint(blob, 1700000000) // timestamp
	blob = append(blob, bytes.Repeat([]byte{0xaa}, 32)...)
	blob = append(blob, 0, 0, 0, 0) // nonce

	// miner tx
	blob = appendVarint(blob, 2)       // version 2
	blob = appendVarint(blob, 1000060) // unlock_time
	blob = appendVarint(blob, 1)       // vin count
	blob = append(blob, 0xff)          // gen
	blob = appendVarint(blob, 1000000) // height
	blob = appendVarint(blob, 1)       // vout count
	blob = appendVarint(blob, 600000000000)
	blob = append(blob, 0x03)                                // tagged key
	blob = append(blob, bytes.Repeat([]byte{0xbb}, 33)...)   // key + view tag
	extra := append([]byte{0x01}, bytes.Repeat([]byte{0xcc}, 32)...) // tx pubkey
	extra = append(extra, 0x02, 8)                           // extra_nonce tag + len
	extra = append(extra, make([]byte, 8)...)                // reserved space
	blob = appendVarint(blob, uint64(len(extra)))
	blob = append(blob, extra...)
	blob = append(blob, 0x00) // RCTTypeNull

	blob = appendVarint(blob, uint64(len(txHashes)))
	for _, h := range txHashes {
		blob = append(blob, h...)
	}
	return blob
}

func TestParseAndHashingBlob(t *testing.T) {
	txHashes := [][]byte{
		bytes.Repeat([]byte{0x11}, 32),
		bytes.Repeat([]byte{0x22}, 32),
	}
	blob := buildSyntheticBlob(t, txHashes)

	p, err := parseBlockBlob(blob)
	if err != nil {
		t.Fatal(err)
	}
	if p.minerTxVer != 2 || p.txCount != 2 {
		t.Fatalf("parse wrong: ver=%d txs=%d", p.minerTxVer, p.txCount)
	}
	// nonce must be the 4 bytes right before headerEnd
	if p.headerEnd-p.nonceOffset != 4 {
		t.Fatalf("nonce span wrong: %d..%d", p.nonceOffset, p.headerEnd)
	}

	hb := p.hashingBlob()
	// hashing blob = header + 32-byte root + varint(3)
	if len(hb) != p.headerEnd+32+1 {
		t.Fatalf("hashing blob length wrong: %d", len(hb))
	}
	if hb[len(hb)-1] != 3 { // tx count incl. miner
		t.Fatalf("hashing blob tx count wrong: %d", hb[len(hb)-1])
	}

	// the root must equal tree_hash(minerTxHash, tx1, tx2) computed directly
	root := treeHash([][]byte{p.minerTxHash(), txHashes[0], txHashes[1]})
	if !bytes.Equal(hb[p.headerEnd:p.headerEnd+32], root) {
		t.Fatal("hashing blob root mismatch")
	}

	// poking the reserved extra bytes must change the miner tx hash and root
	blob2 := append([]byte(nil), blob...)
	copy(blob2[p.minerTxEnd-9:p.minerTxEnd-1], []byte("EXTRANON")) // inside extra_nonce
	p2, err := parseBlockBlob(blob2)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(p2.minerTxHash(), p.minerTxHash()) {
		t.Fatal("extranonce change must alter the miner tx hash")
	}

	// withNonce must only touch the 4 nonce bytes
	n := withNonce(blob, p.nonceOffset, []byte{1, 2, 3, 4})
	if !bytes.Equal(n[p.nonceOffset:p.nonceOffset+4], []byte{1, 2, 3, 4}) {
		t.Fatal("nonce not written")
	}
	if !bytes.Equal(n[:p.nonceOffset], blob[:p.nonceOffset]) || !bytes.Equal(n[p.headerEnd:], blob[p.headerEnd:]) {
		t.Fatal("withNonce must not touch other bytes")
	}
}

func TestMinerTxHashV2Structure(t *testing.T) {
	blob := buildSyntheticBlob(t, nil)
	p, err := parseBlockBlob(blob)
	if err != nil {
		t.Fatal(err)
	}
	tx := blob[p.headerEnd:p.minerTxEnd]
	want := keccak256(keccak256(tx[:len(tx)-1]), keccak256([]byte{0x00}), make([]byte, 32))
	if !bytes.Equal(p.minerTxHash(), want) {
		t.Fatal("v2 miner tx 3-part hash mismatch")
	}
}
