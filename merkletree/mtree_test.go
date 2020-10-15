package merkletree

import (
	"encoding/hex"
	"testing"
)

func TestNewMerkleTree(t *testing.T) {
	mt1 := NewMerkleTree([][]byte{[]byte("hello"), []byte("world")})

	if hex.EncodeToString(mt1.WithFirst([]byte("first"))) != "11f206ce3848f46083c5f30d01b95a8dd75194ef5781b24202d34720b2b4c12f" {
		t.Fail()
	}

	if GetMerkleHashes(mt1.Steps)[0] != "776f726c64" {
		t.Log(GetMerkleHashes(mt1.Steps)[0])
		t.Fail()
	}
}
