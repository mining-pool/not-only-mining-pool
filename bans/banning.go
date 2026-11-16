package bans

import (
	"sync"
	"time"

	"github.com/mining-pool/not-only-mining-pool/config"
)

// BanningManager tracks temporarily banned miner IPs. It is shared by every
// client goroutine, so all state is guarded by mu. Options may be replaced at
// runtime (configuration reload); nil options, or a nil manager, disable
// banning.
type BanningManager struct {
	mu           sync.Mutex
	options      *config.BanningOptions
	bannedIPList map[string]time.Time
	started      bool
}

func NewBanningManager(options *config.BanningOptions) *BanningManager {
	return &BanningManager{
		options:      options,
		bannedIPList: make(map[string]time.Time),
	}
}

// Options returns the banning settings in force, or nil when banning is off.
func (bm *BanningManager) Options() *config.BanningOptions {
	if bm == nil {
		return nil
	}
	bm.mu.Lock()
	defer bm.mu.Unlock()
	return bm.options
}

// Update replaces the banning settings. Disabling banning forgives every ban.
func (bm *BanningManager) Update(options *config.BanningOptions) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.options = options
	if options == nil {
		bm.bannedIPList = make(map[string]time.Time)
	}
}

// Init starts the purge loop once. It follows the current settings, so it
// keeps working when banning is enabled or retuned by a reload.
func (bm *BanningManager) Init() {
	if bm == nil {
		return
	}
	bm.mu.Lock()
	if bm.started {
		bm.mu.Unlock()
		return
	}
	bm.started = true
	bm.mu.Unlock()

	go func() {
		for {
			interval := time.Minute
			if o := bm.Options(); o != nil && o.PurgeInterval > 0 {
				interval = time.Duration(o.PurgeInterval) * time.Second
			}
			time.Sleep(interval)
			bm.purge(time.Now())
		}
	}()
}

func (bm *BanningManager) purge(now time.Time) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.options == nil {
		return
	}
	for ip, at := range bm.bannedIPList {
		if now.Sub(at) > time.Duration(bm.options.Time)*time.Second {
			delete(bm.bannedIPList, ip)
		}
	}
}

// CheckBan reports whether the address is currently banned.
func (bm *BanningManager) CheckBan(strRemoteAddr string) (shouldCloseSocket bool) {
	if bm == nil {
		return false
	}
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.options == nil {
		return false
	}
	at, ok := bm.bannedIPList[strRemoteAddr]
	if !ok {
		return false
	}
	if time.Since(at) < time.Duration(bm.options.Time)*time.Second {
		return true
	}
	delete(bm.bannedIPList, strRemoteAddr) // forgiven
	return false
}

// AddBannedIP bans the address now; a no-op when banning is disabled.
func (bm *BanningManager) AddBannedIP(strRemoteAddr string) {
	if bm == nil {
		return
	}
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.options == nil {
		return
	}
	bm.bannedIPList[strRemoteAddr] = time.Now()
}
