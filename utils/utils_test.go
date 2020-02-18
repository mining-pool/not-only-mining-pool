package utils

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestSerializeNumber(t *testing.T) {
	if bytes.Compare(SerializeNumber(100), []byte{0x01, 0x64}) != 0 {
		fmt.Println(SerializeNumber(100))
		t.Fail()
	}
}

func TestSerializeString(t *testing.T) {
	if bytes.Compare(SerializeString("HelloWorld"), []byte{0x0a, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x57, 0x6f, 0x72, 0x6c, 0x64}) != 0 {
		fmt.Println(SerializeString("HelloWorld"))
		t.Fail()
	}
}

func TestReverseByteOrder(t *testing.T) {
	hash := Sha256([]byte("0000"))
	fmt.Println(hash)
	fmt.Println(ReverseByteOrder(hash))
}

func TestReverseBytes(t *testing.T) {
	hash := Sha256([]byte("0000"))
	fmt.Println(hash)
	fmt.Println(ReverseBytes(hash))
	fmt.Println(hex.EncodeToString(hash))
	fmt.Println(hex.EncodeToString(ReverseBytes(hash)))
}

func TestUint256BytesFromHash(t *testing.T) {
	result, _ := hex.DecodeString("691938264876d1078051da4e30ec0643262e8b93fca661f525fe7122b38d5f18")
	if bytes.Compare(Uint256BytesFromHash(hex.EncodeToString(Sha256([]byte("Hello")))), result) != 0 {
		t.Fail()
	}
}

func TestVarIntBytes(t *testing.T) {
	if hex.EncodeToString(VarIntBytes(uint64(23333))) != "fd255b" {
		t.Fail()
	}
}

func TestVarStringBytes(t *testing.T) {

}
