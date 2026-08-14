package worksource

import (
	"context"
	"testing"
	"time"
)

// TestZMQSubscribeDialFails checks that a dead endpoint surfaces an error
// (so Subscribe reconnects) rather than hanging. ZMTP framing itself is covered
// by the go-zeromq/zmq4 library's own tests.
func TestZMQSubscribeDialFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- ZMQSubscribe(ctx, "tcp://127.0.0.1:1", "hashblock", func([][]byte) {})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error dialing a closed port")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ZMQSubscribe hung on a dead endpoint")
	}
}
