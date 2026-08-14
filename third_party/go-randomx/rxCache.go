package randomx

import "C"
import (
	"bytes"
)

func NewRxCache(flags ...Flag) (*RxCache, error) {
	cache, err := AllocCache(flags...)
	if cache == nil {
		return nil, err
	}

	return &RxCache{cache: cache}, nil
}

func (c *RxCache) Close() {
	if c.cache != nil {
		ReleaseCache(c.cache)
	}
}

func (c *RxCache) Init(seed []byte) bool {
	if c.IsReady(seed) {
		return false
	}

	c.seed = seed
	InitCache(c.cache, c.seed)

	c.initCount++

	return true
}

func (c *RxCache) IsReady(seed []byte) bool {
	return (c.initCount > 0) && (bytes.Compare(c.seed, seed) == 0)
}
