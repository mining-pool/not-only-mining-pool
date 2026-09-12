package quantus

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"math/rand"
	"os"
	"testing"
)

func TestOfficialNonceHashVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/nonce_hash.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct{ Header, Nonce, Hash string }
	if err = json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 5 {
		t.Fatal("missing upstream vectors")
	}
	for _, v := range vectors {
		h, _ := hex.DecodeString(v.Header)
		n, _ := hex.DecodeString(v.Nonce)
		hash := NonceHash([32]byte(h), [64]byte(n))
		if hex.EncodeToString(hash[:]) != v.Hash {
			t.Fatalf("nonce %s: got %x, want %s", v.Nonce, hash, v.Hash)
		}
	}
}
func TestFieldArithmetic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	p := new(big.Int).SetUint64(goldilocks)
	values := []uint64{0, 1, goldilocks - 1, goldilocks - 2, 0xffffffff, 1 << 63}
	for i := 0; i < 100; i++ {
		values = append(values, rng.Uint64()%goldilocks)
	}
	for _, a := range values {
		for _, b := range values {
			x, y := new(big.Int).SetUint64(a), new(big.Int).SetUint64(b)
			sum := new(big.Int).Add(x, y)
			sum.Mod(sum, p)
			prod := new(big.Int).Mul(x, y)
			prod.Mod(prod, p)
			if add(a, b) != sum.Uint64() || mul(a, b) != prod.Uint64() {
				t.Fatalf("arithmetic mismatch %x %x", a, b)
			}
		}
	}
}
