package storage

import (
	"context"
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

// waitZCard polls until the pplnslog reaches n members (the writer is async).
func waitZCard(t *testing.T, db *DB, key string, n int64) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if c, _ := db.ZCard(context.Background(), key).Result(); c == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	c, _ := db.ZCard(context.Background(), key).Result()
	t.Fatalf("pplnslog card = %d, want %d", c, n)
}

// Outside PPS, the log is rank-capped: only the newest pplnsLogCap shares survive.
func TestPPLNS_LogRankCapped(t *testing.T) {
	pplnsLogCap.Store(3)
	defer pplnsLogCap.Store(200000)
	db, _ := newTestDB(t) // ppsMode false by default
	for i := 0; i < 8; i++ {
		db.PutShare(&types.Share{Miner: "A", Rig: "r", Diff: 1, BlockHeight: 100}, false)
	}
	waitZCard(t, db, "T:shares:pplnslog", 3) // trimmed to the cap
}

// In PPS the rank cap is NOT applied — no uncredited share is dropped even past
// the cap; the log is trimmed by the payout cursor instead.
func TestPPS_LogRetainedUntilCredited(t *testing.T) {
	pplnsLogCap.Store(3)
	defer pplnsLogCap.Store(200000)
	db, _ := newTestDB(t)
	db.SetPPSMode(true)
	for i := 0; i < 8; i++ {
		db.PutShare(&types.Share{Miner: "A", Rig: "r", Diff: 1, BlockHeight: 100}, false)
	}
	waitZCard(t, db, "T:shares:pplnslog", 8) // all kept despite cap=3

	// crediting up to seq 5 drops the credited shares, keeping the uncredited tail.
	if err := db.ApplyPayments(&PaymentUpdate{PPSCursor: 5}); err != nil {
		t.Fatal(err)
	}
	if c, _ := db.ZCard(context.Background(), "T:shares:pplnslog").Result(); c != 3 {
		t.Errorf("after crediting to seq 5, pplnslog card = %d, want 3 (seq 6-8)", c)
	}
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
