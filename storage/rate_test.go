package storage

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/mining-pool/not-only-mining-pool/types"
)

func waitShares(t *testing.T, db *DB, n int64) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if c, _ := db.ZCard(context.Background(), "T:pool:shares").Result(); c == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("pool share series never reached %d entries", n)
}

func TestWorkRatesCountEveryShareInTheWindow(t *testing.T) {
	db, _ := newTestDB(t)
	ctx := context.Background()
	now := time.Now().Unix()

	// A pre-fix entry (bare difficulty member) two minutes ago and one older
	// than the retention period, which the next write must trim.
	db.ZAdd(ctx, "T:pool:shares", &redis.Z{Score: float64(now - 120), Member: "6.00000"})
	db.ZAdd(ctx, "T:pool:shares", &redis.Z{Score: float64(now - rateRetention - 10), Member: "1000.00000"})

	// Fixed-difficulty ports submit many equal shares; each must count.
	for i := 0; i < 4; i++ {
		db.PutShare(&types.Share{Miner: "alice", Rig: "r1", Diff: 8}, true)
	}
	db.PutShare(&types.Share{Miner: "alice", Rig: "r2", Diff: 2}, true)
	db.PutShare(&types.Share{Miner: "bob", Rig: "r1", Diff: 4}, true)
	waitShares(t, db, 7) // 6 new + the legacy entry; the expired one is gone

	end := time.Now().Unix()
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

	pool, err := db.GetPoolHashrate(end-60, end)
	if err != nil || !near(pool, 38.0/60) {
		t.Fatalf("pool 1m rate = %v (%v), want 38/60", pool, err)
	}
	pool30, _ := db.GetPoolHashrate(end-30*60, end)
	if !near(pool30, 44.0/(30*60)) {
		t.Fatalf("pool 30m rate = %v, want legacy entry included", pool30)
	}
	miner, err := db.GetMinerHashrate("alice", end-60, end)
	if err != nil || !near(miner, 34.0/60) {
		t.Fatalf("miner rate = %v (%v), want 34/60", miner, err)
	}
	rig, _ := db.GetRigHashrate("alice", "r1", end-60, end)
	if !near(rig, 32.0/60) {
		t.Fatalf("rig rate = %v, want 32/60", rig)
	}
	if empty, _ := db.GetMinerHashrate("carol", end-60, end); empty != 0 {
		t.Fatalf("unknown miner rate = %v", empty)
	}
	if old, _ := db.ZCount(ctx, "T:pool:shares", "-inf", "("+strconv.FormatInt(now-rateRetention, 10)).Result(); old != 0 {
		t.Fatal("expired samples were not trimmed")
	}
}
