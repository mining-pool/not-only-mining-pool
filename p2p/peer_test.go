package p2p

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mining-pool/not-only-mining-pool/config"
)

func TestNewPeer(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a live p2p node on :19335")
	}
	var options config.P2POptions
	json.Unmarshal([]byte(`
{
    "host": "0.0.0.0",
    "port": 19335,
    "magic": "fdd2c8f1",
    "disableTransactions": true
}
`), &options)
	peer := NewPeer(70015, &options)
	peer.Init()

	c := time.After(time.Minute)
	for {
		select {
		case <-c:
			return
		}
	}
}
