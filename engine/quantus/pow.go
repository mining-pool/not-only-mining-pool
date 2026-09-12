package quantus

import (
	"encoding/binary"
	"math/bits"
)

const goldilocks uint64 = 0xffffffff00000001

func add(a, b uint64) uint64 {
	s, carry := bits.Add64(a, b, 0)
	if carry != 0 {
		s += 0xffffffff
	}
	if s >= goldilocks {
		s -= goldilocks
	}
	return s
}

func mul(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	_, rem := bits.Div64(hi, lo, goldilocks)
	return rem
}

func exp7(a uint64) uint64 {
	a2 := mul(a, a)
	return mul(mul(mul(a2, a2), a2), a)
}

func external(s *[12]uint64) {
	for i := 0; i < 12; i += 4 {
		a, b, c, d := s[i], s[i+1], s[i+2], s[i+3]
		ab, cd := add(a, b), add(c, d)
		sum := add(ab, cd)
		x, y := add(sum, b), add(sum, d)
		s[i], s[i+1], s[i+2], s[i+3] = add(x, ab), add(x, add(c, c)), add(y, cd), add(y, add(a, a))
	}
	var sums [4]uint64
	for i, v := range s {
		sums[i%4] = add(sums[i%4], v)
	}
	for i := range s {
		s[i] = add(s[i], sums[i%4])
	}
}

func permute(s *[12]uint64) {
	external(s)
	full := func(constants [4][12]uint64) {
		for _, r := range constants {
			for i := range s {
				s[i] = exp7(add(s[i], r[i]))
			}
			external(s)
		}
	}
	full(initialConstants)
	for _, rc := range internalConstants {
		s[0] = exp7(add(s[0], rc))
		var sum uint64
		for _, v := range s {
			sum = add(sum, v)
		}
		for i := range s {
			s[i] = add(sum, mul(s[i], matrixDiag[i]))
		}
	}
	full(terminalConstants)
}

// NonceHash implements qpow_math::get_nonce_hash: 4-byte little-endian input
// limbs, serialization terminator + sponge padding, and two 32-byte squeezes.
// The returned bytes are interpreted as a big-endian U512 for target comparison.
func NonceHash(header [32]byte, nonce [64]byte) [64]byte {
	var input [96]byte
	copy(input[:], header[:])
	copy(input[32:], nonce[:])
	var state [12]uint64
	for block := 0; block < 3; block++ {
		for i := 0; i < 8; i++ {
			state[i] = add(state[i], uint64(binary.LittleEndian.Uint32(input[block*32+i*4:])))
		}
		permute(&state)
	}
	state[0] = add(state[0], 1)
	state[1] = add(state[1], 1)
	var out [64]byte
	for half := 0; half < 2; half++ {
		permute(&state)
		for i := 0; i < 4; i++ {
			binary.LittleEndian.PutUint64(out[half*32+i*8:], state[i])
		}
	}
	return out
}
