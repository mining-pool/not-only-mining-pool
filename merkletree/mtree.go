package merkletree

import (
	"bytes"
	"encoding/hex"
	"github.com/node-standalone-pool/go-pool-server/utils"
)

type MerkleTree struct {
	Data  interface{}
	Steps [][]byte
}

func NewMerkleTree(data [][]byte) *MerkleTree {
	return &MerkleTree{
		Data:  data,
		Steps: CalculateSteps(data),
	}
}

func CalculateSteps(data [][]byte) [][]byte {
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
			Ld[i] = MerkleJoin(L[r[i]], L[r[i]+1])
		}
		L = append(PreL, Ld...)
		Ll = len(L)
	}

	return steps
}

func MerkleJoin(h1, h2 []byte) []byte {
	return utils.Sha256d(bytes.Join([][]byte{h1, h2}, nil))
}

func (mt *MerkleTree) WithFirst(f []byte) []byte {
	for i := 0; i < len(mt.Steps); i++ {
		f = utils.Sha256d(bytes.Join([][]byte{f, mt.Steps[i]}, nil))
	}
	return f
}

func GetMerkleHashes(steps [][]byte) []string {
	var hashes []string
	for i := 0; i < len(steps); i++ {
		hashes = append(hashes, hex.EncodeToString(steps[i]))
	}
	return hashes
}
