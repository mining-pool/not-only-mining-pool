package randomx

import (
	"encoding/hex"
	"testing"
)

// Official RandomX test vector (tevador/RandomX tests):
// key "test key 000", input "This is a test"
func TestLightVMOfficialVector(t *testing.T) {
	vm, err := NewLightVM([]byte("test key 000"))
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()

	h, err := vm.Hash([]byte("test key 000"), []byte("This is a test"))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(h) != "639183aae1bf4c9a35884cb46b09cad9175f04efd7684e7262a0ac1c2f0b4e3f" {
		t.Fatalf("official vector mismatch: %x", h)
	}

	// second vector, same key: "Lorem ipsum dolor sit amet"
	h2, _ := vm.Hash([]byte("test key 000"), []byte("Lorem ipsum dolor sit amet"))
	if hex.EncodeToString(h2) != "300a0adb47603dedb42228ccb2b211104f4da45af709cd7547cd049e9489c969" {
		t.Fatalf("second vector mismatch: %x", h2)
	}

	// re-key vector: key "test key 001", input "Test"... use documented one:
	// key "test key 001", input "This is a test" -> e9ff... (not in README);
	// instead assert re-keying works and changes the output.
	h3, err := vm.Hash([]byte("test key 001"), []byte("This is a test"))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(h3) == hex.EncodeToString(h) {
		t.Fatal("different seed must change the hash")
	}
}
