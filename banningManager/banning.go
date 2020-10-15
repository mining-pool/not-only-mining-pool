package banningManager

import (
	"time"

	"github.com/mining-pool/not-only-mining-pool/config"
)

type BanningManager struct {
	Options      *config.BanningOptions
	BannedIPList map[string]*time.Time
}

func NewBanningManager(options *config.BanningOptions) *BanningManager {
	return &BanningManager{
		Options:      options,
		BannedIPList: make(map[string]*time.Time),
	}
}

func (bm *BanningManager) Init() {
	go func() {
		ticker := time.NewTicker(time.Duration(bm.Options.PurgeInterval) * time.Second)
		defer ticker.Stop()

		for {
			<-ticker.C
			for ip, banTime := range bm.BannedIPList {
				if time.Since(*banTime) > time.Duration(bm.Options.Time)*time.Second {
					delete(bm.BannedIPList, ip)
				}
			}
		}
	}()
}

func (bm *BanningManager) CheckBan(strRemoteAddr string) (shouldCloseSocket bool) {
	if bm.BannedIPList[strRemoteAddr] != nil {
		bannedTime := bm.BannedIPList[strRemoteAddr]
		bannedTimeAgo := time.Since(*bannedTime)
		timeLeft := time.Duration(bm.Options.Time)*time.Second - bannedTimeAgo
		if timeLeft > 0 {
			return true
			// client.Socket.Close()
			// kickedBannedIP
		} else {
			delete(bm.BannedIPList, strRemoteAddr)
			// forgaveBannedIP
		}
	}

	return false
}

func (bm *BanningManager) AddBannedIP(strRemoteAddr string) {
	now := time.Now()
	bm.BannedIPList[strRemoteAddr] = &now
}
