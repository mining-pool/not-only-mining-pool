//go:build randomx
// +build randomx

package cryptonote

// Linked only with `-tags randomx`: links the prebuilt librandomx.a shipped in
// github.com/mining-pool/go-randomx (RandomX v1.2.1).
import randomx "github.com/mining-pool/go-randomx"

func init() {
	newPowHasher = func() (powHasher, error) {
		return &rxHasher{}, nil
	}
}

// rxHasher adapts the light-mode RandomX VM to the engine's powHasher. The VM
// is created lazily on the first Hash call (seed comes from the first block
// template) and re-keys itself automatically when the seed rotates.
type rxHasher struct {
	vm *randomx.LightVM
}

func (h *rxHasher) Hash(seed, input []byte) ([]byte, error) {
	if h.vm == nil {
		vm, err := randomx.NewLightVM(seed)
		if err != nil {
			return nil, err
		}
		h.vm = vm
	}
	return h.vm.Hash(seed, input)
}

func (h *rxHasher) Close() {
	if h.vm != nil {
		h.vm.Close()
	}
}
