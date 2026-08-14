package cryptonote

import (
	"errors"

	"golang.org/x/crypto/sha3"
)

// This file implements the CryptoNote consensus serialization pieces a pool
// needs: keccak-256, the Monero tree_hash, block-blob parsing (varint walking
// to locate the nonce and the miner tx span), the 3-part miner tx hash, and
// hashing-blob reconstruction after the pool pokes its extranonce into the
// reserved space. tree_hash is a faithful port of monero src/crypto/tree-hash.c
// and is pinned by vectors generated from that exact C code (see blob_test.go).

func keccak256(data ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, d := range data {
		h.Write(d)
	}
	return h.Sum(nil)
}

// treeHash ports monero's tree_hash (tree-hash.c). Note its unusual shape: the
// leaf layer is only partially compressed so the total collapses to a power of
// two, and the last step hashes the final pair.
func treeHash(hashes [][]byte) []byte {
	switch len(hashes) {
	case 0:
		return nil
	case 1:
		out := make([]byte, 32)
		copy(out, hashes[0])
		return out
	case 2:
		return keccak256(hashes[0], hashes[1])
	}

	count := len(hashes)
	pow := 2
	for pow < count {
		pow <<= 1
	}
	cnt := pow >> 1 // cnt < count <= 2*cnt

	ints := make([][]byte, cnt)
	// copy the first 2*cnt-count leaves untouched
	for i := 0; i < 2*cnt-count; i++ {
		ints[i] = hashes[i]
	}
	// compress the remaining leaves pairwise into the tail slots
	for i, j := 2*cnt-count, 2*cnt-count; j < cnt; i, j = i+2, j+1 {
		ints[j] = keccak256(hashes[i], hashes[i+1])
	}
	// standard binary reduction down to 2, then the root
	for cnt > 2 {
		cnt >>= 1
		for i, j := 0, 0; j < cnt; i, j = i+2, j+1 {
			ints[j] = keccak256(ints[i], ints[i+1])
		}
	}
	return keccak256(ints[0], ints[1])
}

// --- varint (unsigned LEB128, as used by CryptoNote serialization) ---

func readVarint(b []byte, pos int) (val uint64, next int, err error) {
	var shift uint
	for {
		if pos >= len(b) {
			return 0, 0, errors.New("varint: unexpected end of blob")
		}
		c := b[pos]
		val |= uint64(c&0x7f) << shift
		pos++
		if c&0x80 == 0 {
			return val, pos, nil
		}
		shift += 7
		if shift > 63 {
			return 0, 0, errors.New("varint: overflow")
		}
	}
}

func appendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// parsedBlock locates the consensus-relevant spans inside a block blob
// (blocktemplate_blob and the final block share this layout).
type parsedBlock struct {
	blob        []byte
	nonceOffset int    // 4-byte header nonce
	headerEnd   int    // end of the block header (== miner tx start)
	minerTxEnd  int    // one past the miner tx bytes
	minerTxVer  uint64 // 1 or 2
	txCount     int    // number of NON-miner txs (hashes listed in the blob)
	txHashes    [][]byte
}

// parseBlockBlob walks a CryptoNote block blob:
//
//	header:   major(varint) minor(varint) timestamp(varint) prev(32) nonce(4)
//	miner tx: version(varint) unlock(varint) vin=1(varint) 0xff height(varint)
//	          vout(varint) [amount(varint) tag key(32) [viewtag(1)]]...
//	          extraLen(varint) extra... [rct type byte if version>=2]
//	tail:     txCount(varint) txHash(32)...
func parseBlockBlob(blob []byte) (*parsedBlock, error) {
	p := &parsedBlock{blob: blob}
	pos := 0
	var err error

	// header
	for i := 0; i < 3; i++ { // major, minor, timestamp
		if _, pos, err = readVarint(blob, pos); err != nil {
			return nil, err
		}
	}
	if pos+36 > len(blob) {
		return nil, errors.New("blob too short for prev+nonce")
	}
	pos += 32
	p.nonceOffset = pos
	pos += 4
	p.headerEnd = pos

	// miner tx prefix
	if p.minerTxVer, pos, err = readVarint(blob, pos); err != nil {
		return nil, err
	}
	if _, pos, err = readVarint(blob, pos); err != nil { // unlock_time
		return nil, err
	}
	var vinCount uint64
	if vinCount, pos, err = readVarint(blob, pos); err != nil {
		return nil, err
	}
	if vinCount != 1 || pos >= len(blob) || blob[pos] != 0xff {
		return nil, errors.New("miner tx must have exactly one gen input")
	}
	pos++
	if _, pos, err = readVarint(blob, pos); err != nil { // height
		return nil, err
	}

	var voutCount uint64
	if voutCount, pos, err = readVarint(blob, pos); err != nil {
		return nil, err
	}
	for i := uint64(0); i < voutCount; i++ {
		if _, pos, err = readVarint(blob, pos); err != nil { // amount
			return nil, err
		}
		if pos >= len(blob) {
			return nil, errors.New("blob too short for vout tag")
		}
		tag := blob[pos]
		pos++
		switch tag {
		case 0x02: // txout_to_key
			pos += 32
		case 0x03: // txout_to_tagged_key (view tags, v15+)
			pos += 33
		default:
			return nil, errors.New("unknown vout target tag in miner tx")
		}
		if pos > len(blob) {
			return nil, errors.New("blob too short for vout key")
		}
	}

	var extraLen uint64
	if extraLen, pos, err = readVarint(blob, pos); err != nil {
		return nil, err
	}
	pos += int(extraLen)
	if pos > len(blob) {
		return nil, errors.New("blob too short for tx extra")
	}

	if p.minerTxVer >= 2 {
		// rct_signatures: RCTTypeNull, a single 0x00 byte for miner txs
		if pos >= len(blob) || blob[pos] != 0x00 {
			return nil, errors.New("miner tx v2 must end with RCTTypeNull byte")
		}
		pos++
	}
	p.minerTxEnd = pos

	// non-miner tx hashes
	var txCount uint64
	if txCount, pos, err = readVarint(blob, pos); err != nil {
		return nil, err
	}
	p.txCount = int(txCount)
	p.txHashes = make([][]byte, 0, txCount)
	for i := uint64(0); i < txCount; i++ {
		if pos+32 > len(blob) {
			return nil, errors.New("blob too short for tx hashes")
		}
		p.txHashes = append(p.txHashes, blob[pos:pos+32])
		pos += 32
	}
	if pos != len(blob) {
		return nil, errors.New("trailing bytes after block blob")
	}
	return p, nil
}

// minerTxHash computes the transaction hash of the miner tx span. For v2 txs
// it is the CryptoNote 3-part hash keccak(keccak(prefix) || keccak(rctBase) ||
// zero32) where the miner tx's rct base is the single RCTTypeNull byte and the
// prunable part is defined as all-zero; v1 txs hash the whole blob.
func (p *parsedBlock) minerTxHash() []byte {
	tx := p.blob[p.headerEnd:p.minerTxEnd]
	if p.minerTxVer < 2 {
		return keccak256(tx)
	}
	prefix := tx[:len(tx)-1]
	rctBase := tx[len(tx)-1:] // the 0x00 RCTTypeNull byte
	return keccak256(keccak256(prefix), keccak256(rctBase), make([]byte, 32))
}

// hashingBlob rebuilds the block hashing blob for PoW:
// header || tree_root(minerTxHash, txHashes...) || varint(txCount+1)
func (p *parsedBlock) hashingBlob() []byte {
	leaves := make([][]byte, 0, p.txCount+1)
	leaves = append(leaves, p.minerTxHash())
	leaves = append(leaves, p.txHashes...)

	out := make([]byte, 0, p.headerEnd+33)
	out = append(out, p.blob[:p.headerEnd]...)
	out = append(out, treeHash(leaves)...)
	out = appendVarint(out, uint64(p.txCount+1))
	return out
}

// withNonce returns a copy of the blob with the 4-byte header nonce replaced.
func withNonce(blob []byte, nonceOffset int, nonce []byte) []byte {
	out := append([]byte(nil), blob...)
	copy(out[nonceOffset:nonceOffset+4], nonce)
	return out
}
