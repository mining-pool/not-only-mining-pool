package storage

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/types"
)

func newTestDB(t *testing.T) (*DB, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	host, portStr, _ := net.SplitHostPort(mr.Addr())
	port, _ := strconv.Atoi(portStr)
	return NewStorage("T", &config.RedisOptions{Network: "tcp", Host: host, Port: port}), mr
}

// The single ordered writer must seal a block's round with exactly the shares
// recorded before it: shares enqueued before the block land in round<height>,
// the block's own share is included, and shares after it start the next round.
func TestPutShare_RoundSealIsOrdered(t *testing.T) {
	db, mr := newTestDB(t)

	share := func(miner string, diff float64) *types.Share {
		return &types.Share{Miner: miner, Rig: "r", Diff: diff, BlockHeight: 100}
	}
	// round 100: three shares before the block
	db.PutShare(share("A", 1), false)
	db.PutShare(share("B", 2), false)
	db.PutShare(share("A", 1), false)
	// block found at height 100 (a valid share that also solves the block)
	blk := share("A", 1)
	blk.BlockHex, blk.BlockHash, blk.TxHash = "00", "blkhash", "tx"
	db.PutShare(blk, true)
	// shares after the block belong to the next round
	db.PutShare(&types.Share{Miner: "C", Rig: "r", Diff: 5, BlockHeight: 101}, false)

	// wait for the async writer to seal round 100 and start the next round.
	for i := 0; i < 200; i++ {
		if v, _ := mr.Get("T:shares:round100"); mr.Exists("T:shares:round100") || v != "" {
			if c := mr.HGet("T:shares:roundCurrent", "C"); c != "" {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := mr.HGet("T:shares:round100", "A"); got != "3" {
		t.Errorf("round100 minerA = %q, want 3 (two shares + the block share)", got)
	}
	if got := mr.HGet("T:shares:round100", "B"); got != "2" {
		t.Errorf("round100 minerB = %q, want 2", got)
	}
	// the post-block share must NOT have leaked into the sealed round
	if got := mr.HGet("T:shares:round100", "C"); got != "" {
		t.Errorf("round100 minerC = %q, want empty (submitted after the block)", got)
	}
	// ...and must be in the new current round instead
	if got := mr.HGet("T:shares:roundCurrent", "C"); got != "5" {
		t.Errorf("roundCurrent minerC = %q, want 5", got)
	}
	if got := mr.HGet("T:shares:roundCurrent", "A"); got != "" {
		t.Errorf("roundCurrent minerA = %q, want empty (its shares were sealed)", got)
	}
}
