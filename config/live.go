package config

import "sync/atomic"

// Live holds the running configuration. A reload publishes a complete new
// *Options; readers call Get for each use, so they never observe a half-applied
// change. Values in a published Options must not be mutated afterwards.
type Live struct {
	p atomic.Pointer[Options]
}

// NewLive starts with the given configuration.
func NewLive(o *Options) *Live {
	l := &Live{}
	l.p.Store(o)
	return l
}

// Get returns the current configuration.
func (l *Live) Get() *Options { return l.p.Load() }

// Set publishes a new configuration.
func (l *Live) Set(o *Options) { l.p.Store(o) }
