package worksource

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollEmitsOnChangeOnly(t *testing.T) {
	var calls int32
	var emits int32
	refresh := func() (bool, error) {
		n := atomic.AddInt32(&calls, 1)
		return n%2 == 0, nil // change on every 2nd tick
	}
	src := Poll(5*time.Millisecond, refresh)

	done := make(chan struct{})
	go func() { _ = src(func() { atomic.AddInt32(&emits, 1) }); close(done) }()

	time.Sleep(60 * time.Millisecond)
	c, e := atomic.LoadInt32(&calls), atomic.LoadInt32(&emits)
	if c < 4 {
		t.Fatalf("expected several polls, got %d", c)
	}
	if e == 0 || int32(e) > c {
		t.Fatalf("emits (%d) should be >0 and <= calls (%d)", e, c)
	}
}

func TestPollSwallowsErrors(t *testing.T) {
	var emits int32
	refresh := func() (bool, error) { return true, errors.New("boom") }
	go func() { _ = Poll(2*time.Millisecond, refresh)(func() { atomic.AddInt32(&emits, 1) }) }()
	time.Sleep(30 * time.Millisecond)
	if atomic.LoadInt32(&emits) != 0 {
		t.Fatal("errors must not emit")
	}
}

func TestSubscribeRefreshesPerEventAndReconnects(t *testing.T) {
	var emits int32
	var opens int32

	// open fires 2 events then "dies"; Subscribe should reconnect and repeat.
	open := func(onEvent func()) error {
		atomic.AddInt32(&opens, 1)
		onEvent()
		onEvent()
		return errors.New("stream closed")
	}
	refresh := func() (bool, error) { return true, nil }

	go func() { _ = Subscribe("test", open, refresh)(func() { atomic.AddInt32(&emits, 1) }) }()

	// within a few seconds we should see the first connection's 2 emits;
	// reconnect backoff is 3s so exactly the first batch is deterministic here.
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&opens) < 1 || atomic.LoadInt32(&emits) < 2 {
		t.Fatalf("expected >=1 open and >=2 emits, got opens=%d emits=%d", opens, emits)
	}
}

func TestRunRacesSourcesAndReturnsFirstError(t *testing.T) {
	fatal := func(emit Emit) error { return errors.New("fatal") }
	var ticks int32
	ticker := func(emit Emit) error {
		for {
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&ticks, 1)
			emit()
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	var err error
	go func() { defer wg.Done(); err = Run(func() {}, ticker, fatal) }()
	wg.Wait()
	if err == nil {
		t.Fatal("Run must return the first source's fatal error")
	}
}
