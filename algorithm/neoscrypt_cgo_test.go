//go:build neoscrypt
// +build neoscrypt

package algorithm

import (
	"bytes"
	"testing"
)

// Known-answer vector from github.com/sparkspay/go-neoscrypt (profile 0, the
// Feathercoin parameters), routed through the registry.
func TestNeoscryptKAT(t *testing.T) {
	in := []byte("00000000000000000000000000000000000000000000000000000000000000000000000000000000")
	want := []byte{110, 239, 215, 250, 64, 118, 73, 49, 66, 86, 57, 94, 63, 230, 30, 131,
		61, 28, 81, 148, 226, 99, 103, 87, 251, 112, 186, 1, 144, 167, 192, 147}
	if got := GetHashFunc("neoscrypt")(in); !bytes.Equal(got, want) {
		t.Fatalf("neoscrypt KAT mismatch:\n got %v\nwant %v", got, want)
	}
	if !IsSupported("neoscrypt") {
		t.Fatal("neoscrypt should be registered under -tags neoscrypt")
	}
}
