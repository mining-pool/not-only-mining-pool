package banningManager

import (
	"github.com/mining-pool/go-pool-server/config"
	"time"
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
		ch := time.Tick(time.Duration(bm.Options.PurgeInterval) * time.Second)
		for {
			select {
			case <-ch:
				for ip, banTime := range bm.BannedIPList {
					if time.Now().Sub(*banTime) > time.Duration(bm.Options.Time)*time.Second {
						delete(bm.BannedIPList, ip)
					}
				}
			}
		}
	}()
}

func (bm *BanningManager) CheckBan(strRemoteAddr string) (shouldCloseSocket bool) {
	if bm.BannedIPList[strRemoteAddr] != nil {
		bannedTime := bm.BannedIPList[strRemoteAddr]
		bannedTimeAgo := time.Now().Sub(*bannedTime)
		timeLeft := time.Duration(bm.Options.Time)*time.Second - bannedTimeAgo
		if timeLeft > 0 {
			return true
			//client.Socket.Close()
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
