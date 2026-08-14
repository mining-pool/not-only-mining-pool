package randomx

import (
	"bytes"
	"errors"
	"sync"
)

// LightVM is a cache-only (light mode) RandomX VM intended for pool-side share
// verification: ~256MB per seed instead of the 2GB+ mining dataset, hashing at
// verification speed (~ms). It re-keys itself when the seed changes (Monero
// rotates the RandomX seed every 2048 blocks) and is safe for concurrent use —
// randomx_calculate_hash is NOT thread-safe per VM, so calls are serialized.
type LightVM struct {
	mu    sync.Mutex
	flags Flag
	seed  []byte
	cache *RxCache
	vm    *RxVM
}

// NewLightVM creates a light-mode VM keyed to seed. Pass no flags to use the
// machine-recommended set from GetFlags().
func NewLightVM(seed []byte, flags ...Flag) (*LightVM, error) {
	f := GetFlags()
	if len(flags) > 0 {
		f = FlagDefault
		for _, fl := range flags {
			f |= fl
		}
	}
	f &^= FlagFullMEM // light mode by definition

	l := &LightVM{flags: f}
	if err := l.rekey(seed); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *LightVM) rekey(seed []byte) error {
	if len(seed) == 0 {
		return errors.New("randomx seed must not be empty")
	}

	cache, err := AllocCache(l.flags)
	if err != nil {
		return err
	}
	InitCache(cache, seed)

	vm, err := CreateVM(cache, nil, l.flags)
	if err != nil {
		ReleaseCache(cache)
		return err
	}

	// release previous generation
	if l.vm != nil {
		DestroyVM(l.vm.vm)
	}
	if l.cache != nil {
		ReleaseCache(l.cache.cache)
	}

	l.seed = append([]byte(nil), seed...)
	l.cache = &RxCache{cache: cache, seed: l.seed}
	l.vm = &RxVM{vm: vm}
	return nil
}

// Hash computes RandomX(input) under the given seed, re-keying the VM first if
// the seed differs from the current one.
func (l *LightVM) Hash(seed, input []byte) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !bytes.Equal(seed, l.seed) {
		if err := l.rekey(seed); err != nil {
			return nil, err
		}
	}
	return CalculateHash(l.vm.vm, input), nil
}

// Close releases the VM and cache.
func (l *LightVM) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.vm != nil {
		DestroyVM(l.vm.vm)
		l.vm = nil
	}
	if l.cache != nil {
		ReleaseCache(l.cache.cache)
		l.cache = nil
	}
}
