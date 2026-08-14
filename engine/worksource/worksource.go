// Package worksource makes an engine's "how do I learn about new work" a
// pluggable, EVENT-FIRST component. Polling is demoted to an explicit fallback
// strategy rather than the default assumption.
//
// An engine composes Sources and hands them to Run:
//
//	worksource.Run(emit,
//	    worksource.Subscribe("kaspad-grpc", openStream, e.refresh), // real events
//	    worksource.Poll(5*time.Second, e.refresh),                  // safety net
//	)
//
// Racing a real event Source against a slow Poll gives the best of both: instant
// reaction to node events, plus a guarantee that a dropped/failed subscription
// can never wedge the pool.
package worksource

import (
	"time"

	logging "github.com/ipfs/go-log/v2"
)

var log = logging.Logger("worksource")

// Emit signals the shared layer to re-broadcast work to miners.
type Emit func()

// Refresh pulls the latest template/candidate and updates engine state. It
// returns changed==true when miners must be re-notified. Errors are logged and
// treated as "no change".
type Refresh func() (changed bool, err error)

// Source is a long-running work watcher; it calls emit whenever new work is
// available and returns only on a fatal (unrecoverable) error.
type Source func(emit Emit) error

// Poll builds a polling Source (the FALLBACK strategy): every interval it runs
// refresh and emits on change. Use only for nodes without a push API, or as a
// safety net raced beside a real event Source.
func Poll(interval time.Duration, refresh Refresh) Source {
	return func(emit Emit) error {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			changed, err := refresh()
			if err != nil {
				log.Debug("poll refresh error: ", err)
				continue
			}
			if changed {
				emit()
			}
		}
		return nil
	}
}

// Subscribe builds an EVENT-driven Source. open blocks, invoking onEvent once
// per node notification, and returns when the subscription dies; Subscribe then
// reconnects with a fixed backoff. Each onEvent runs refresh and emits on
// change, so spurious events cost only a refresh.
func Subscribe(name string, open func(onEvent func()) error, refresh Refresh) Source {
	return func(emit Emit) error {
		onEvent := func() {
			changed, err := refresh()
			if err != nil {
				log.Debug(name, " refresh error: ", err)
				return
			}
			if changed {
				emit()
			}
		}
		for {
			err := open(onEvent) // blocks until the stream ends
			log.Warn(name, " subscription ended, reconnecting in 3s: ", err)
			time.Sleep(3 * time.Second)
		}
	}
}

// Run races the given Sources concurrently against a single Emit and returns
// when the first Source returns (a fatal error). With no Sources it blocks
// forever (nothing to watch).
func Run(emit Emit, sources ...Source) error {
	if len(sources) == 0 {
		select {}
	}
	errCh := make(chan error, len(sources))
	for _, s := range sources {
		s := s
		go func() { errCh <- s(emit) }()
	}
	return <-errCh
}
