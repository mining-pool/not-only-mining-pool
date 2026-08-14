package merkletree

import (
	"bytes"
	"encoding/hex"

	"github.com/mining-pool/not-only-mining-pool/utils"
)

// Hasher folds two nodes / hashes a leaf. Most coins use double-SHA256, but some
// (e.g. Groestlcoin) build the transaction merkle tree with single SHA256.
type Hasher func([]byte) []byte

type MerkleTree struct {
	Data   interface{}
	Steps  [][]byte
	hasher Hasher
}

// NewMerkleTree receives a list of tx raw bytes and a node hasher. A nil hasher
// defaults to double-SHA256 (the Bitcoin convention).
func NewMerkleTree(data [][]byte, hasher Hasher) *MerkleTree {
	if hasher == nil {
		hasher = utils.Sha256d
	}
	return &MerkleTree{
		Data:   data,
		Steps:  CalculateSteps(data, hasher),
		hasher: hasher,
	}
}

func CalculateSteps(data [][]byte, hasher Hasher) [][]byte {
	if hasher == nil {
		hasher = utils.Sha256d
	}
	L := data
	steps := make([][]byte, 0)
	PreL := [][]byte{nil}
	StartL := 2
	Ll := len(L)

	for Ll > 1 {
		steps = append(steps, L[1])

		if Ll%2 != 0 {
			L = append(L, L[len(L)-1])
		}

		r := utils.Range(StartL, Ll, 2)
		Ld := make([][]byte, len(r))

		for i := 0; i < len(r); i++ {
			Ld[i] = hasher(bytes.Join([][]byte{L[r[i]], L[r[i]+1]}, nil))
		}
		L = append(PreL, Ld...)
		Ll = len(L)
	}

	return steps
}

func (mt *MerkleTree) WithFirst(f []byte) []byte {
	// mt.hasher is normalized to a non-nil func by NewMerkleTree.
	for i := 0; i < len(mt.Steps); i++ {
		f = mt.hasher(bytes.Join([][]byte{f, mt.Steps[i]}, nil))
	}
	return f
}

func GetMerkleHashes(steps [][]byte) []string {
	hashes := make([]string, len(steps))
	for i := 0; i < len(steps); i++ {
		hashes[i] = hex.EncodeToString(steps[i])
	}

	return hashes
}
