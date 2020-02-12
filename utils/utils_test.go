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
