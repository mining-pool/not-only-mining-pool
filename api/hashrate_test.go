package api

import (
	"encoding/json"
	"math"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/storage"
	"github.com/mining-pool/not-only-mining-pool/types"
)

func serverWithShares(t *testing.T, override float64) *Server {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	host, port, _ := net.SplitHostPort(mr.Addr())
	p, _ := strconv.Atoi(port)
	db := storage.NewStorage("T", &config.RedisOptions{Network: "tcp", Host: host, Port: p})
	for i := 0; i < 3; i++ {
		db.PutShare(&types.Share{Miner: "alice", Rig: "r1", Diff: 8}, true)
	}
	// The share writer is asynchronous; wait until all three are stored.
	for i := 0; i < 200; i++ {
		if members, _ := mr.ZMembers("T:pool:shares"); len(members) == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	s, err := NewServer(&config.Options{API: &config.APIOptions{Host: "127.0.0.1", Port: 1, HashesPerDifficulty: override}}, db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type rateReply struct {
	Hashrate1Min float64 `json:"hashrate1min"`
	Unit         string  `json:"hashrateUnit"`
}

func getRate(t *testing.T, s *Server, path string) rateReply {
	t.Helper()
	var r rateReply
	if err := json.Unmarshal(call(s, "GET", path, "", nil, "").Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRatesAreReportedAsHashrate(t *testing.T) {
	s := serverWithShares(t, 0)
	s.SetHashrate(1000, "Sol/s")
	// 3 shares × difficulty 8 in the last minute × 1000 per difficulty / 60 s.
	want := 3 * 8 * 1000.0 / 60
	for _, path := range []string{"/api/pool", "/api/miner/alice", "/api/miner/alice/rig/r1"} {
		r := getRate(t, s, path)
		if math.Abs(r.Hashrate1Min-want) > 1e-6 || r.Unit != "Sol/s" {
			t.Fatalf("%s = %+v, want %v Sol/s", path, r, want)
		}
	}
}

func TestHashesPerDifficultyOverride(t *testing.T) {
	s := serverWithShares(t, 2)
	s.SetHashrate(1000, "H/s") // the operator's setting wins
	if r := getRate(t, s, "/api/pool"); math.Abs(r.Hashrate1Min-3*8*2.0/60) > 1e-6 || r.Unit != "H/s" {
		t.Fatalf("override ignored: %+v", r)
	}
}
