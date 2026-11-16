package bans

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mining-pool/not-only-mining-pool/config"
)

func TestConcurrentBansAndReloads(t *testing.T) {
	bm := NewBanningManager(&config.BanningOptions{Time: 60, InvalidPercent: 50, CheckThreshold: 1})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				ip := "10.0." + strconv.Itoa(g) + "." + strconv.Itoa(i%50)
				bm.AddBannedIP(ip)
				bm.CheckBan(ip)
				if i%100 == 0 {
					bm.purge(time.Now().Add(2 * time.Minute))
					bm.Update(&config.BanningOptions{Time: 60 + i, InvalidPercent: 50, CheckThreshold: 1})
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestDisablingBanningForgivesEveryone(t *testing.T) {
	bm := NewBanningManager(&config.BanningOptions{Time: 600})
	bm.AddBannedIP("10.0.0.1")
	if !bm.CheckBan("10.0.0.1") {
		t.Fatal("ban not recorded")
	}
	bm.Update(nil)
	if bm.CheckBan("10.0.0.1") || bm.Options() != nil {
		t.Fatal("disabled banning still bans")
	}
	bm.AddBannedIP("10.0.0.2")
	bm.Update(&config.BanningOptions{Time: 600})
	if bm.CheckBan("10.0.0.2") {
		t.Fatal("bans recorded while disabled")
	}
	var nilManager *BanningManager
	if nilManager.CheckBan("x") || nilManager.Options() != nil {
		t.Fatal("nil manager must mean no banning")
	}
	nilManager.AddBannedIP("x")
	nilManager.Init()
}
