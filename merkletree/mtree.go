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
	PreL := make([][]byte, 0)
	StartL := 2
	Ll := len(L)

	if Ll > 1 {
		for Ll != 1 {
			steps = append(steps, L[1])

			if Ll%2 != 0 {
				L = append(L, L[len(L)-1])
			}

			Ld := make([][]byte, 0)
			r := utils.Range(StartL, Ll, 2)

			for i := range r {
				Ld = append(Ld, MerkleJoin(L[i], L[i+1]))
			}
			L = append(PreL, Ld...)
			Ll = len(L)
		}
	}

	return steps
}

func MerkleJoin(h1, h2 []byte) []byte {
	return utils.Sha256d(bytes.Join([][]byte{h1, h2}, nil))
}

func (mt *MerkleTree) WithFirst(f []byte) []byte {
	for i := 0; i < len(mt.Steps); i++ {
		f = MerkleJoin(f, mt.Steps[i])
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
