//go:build neoscrypt
// +build neoscrypt

package algorithm

import goneoscrypt "github.com/sparkspay/go-neoscrypt"

// Linked with `-tags neoscrypt` (cgo). Feathercoin's NeoScrypt is profile 0
// (N=128, r=2, FastKDF, ChaCha+Salsa) — identical parameters to cpuminer's
// 0x80000620. The C routine hashes a fixed 80-byte header.
func init() {
	RegisterHash("neoscrypt", 16, func(header []byte) []byte {
		h := goneoscrypt.NeoscryptHash(header, 0)
		return h[:]
	})
}
