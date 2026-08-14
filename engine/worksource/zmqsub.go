package worksource

import (
	"bytes"
	"context"

	"github.com/go-zeromq/zmq4"
)

// ZMQSubscribe dials a ZMQ PUB endpoint (bitcoind `-zmqpub*` / monerod
// `--zmq-pub`, e.g. "tcp://127.0.0.1:28332"), subscribes to topic, and calls
// onMessage for every received multipart message. It blocks until the socket
// fails, so pass it to Subscribe for reconnect handling. ZMTP transport is
// handled by github.com/go-zeromq/zmq4 (pure Go, no libzmq).
func ZMQSubscribe(ctx context.Context, endpoint, topic string, onMessage func(frames [][]byte)) error {
	sub := zmq4.NewSub(ctx)
	defer sub.Close()

	if err := sub.Dial(endpoint); err != nil {
		return err
	}
	if err := sub.SetOption(zmq4.OptionSubscribe, topic); err != nil {
		return err
	}

	for {
		msg, err := sub.Recv()
		if err != nil {
			return err
		}
		if len(msg.Frames) > 0 {
			onMessage(msg.Frames)
		}
	}
}

// ZMQSource builds an event Source from a ZMQ subscription: each message whose
// topic frame matches triggers refresh. Compose it with a Poll safety net via
// Run.
func ZMQSource(name, endpoint, topic string, refresh Refresh) Source {
	return Subscribe(name, func(onEvent func()) error {
		return ZMQSubscribe(context.Background(), endpoint, topic, func(frames [][]byte) {
			if len(frames) > 0 && bytes.HasPrefix(frames[0], []byte(topic)) {
				onEvent()
			}
		})
	}, refresh)
}
