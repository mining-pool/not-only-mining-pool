package stratum

import "sync/atomic"

type Shares struct {
	Valid   uint64
	Invalid uint64
}

func (s *Shares) TotalShares() uint64 {
	return s.Valid + s.Invalid
}

func (s *Shares) BadPercent() float64 {
	return float64(s.Invalid*100) / float64(s.TotalShares())
}

func (s *Shares) Reset() {
	atomic.StoreUint64(&s.Invalid, 0)
	atomic.StoreUint64(&s.Valid, 0)
}
