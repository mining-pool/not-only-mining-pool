package config

import logging "github.com/ipfs/go-log/v2"

var log = logging.Logger("config")

type Options struct {
	DisablePayment bool         `json:"disablePayment"`
	Coin           *CoinOptions `json:"coin"`

	PoolAddress      *Recipient   `json:"poolAddress"`
	RewardRecipients []*Recipient `json:"rewardRecipients"`

	BlockRefreshInterval   int  `json:"blockRefreshInterval"`
	JobRebroadcastTimeout  int  `json:"jobRebroadcastTimeout"`
	ConnectionTimeout      int  `json:"connectionTimeout"`
	EmitInvalidBlockHashes bool `json:"emitInvalidBlockHashes"`
	TCPProxyProtocol       bool `json:"tcpProxyProtocol"` // http://www.haproxy.org/download/1.8/doc/proxy-protocol.txt

	API       *APIOptions          `json:"api"`
	Banning   *BanningOptions      `json:"banning"`
	Ports     map[int]*PortOptions `json:"ports"`
	Daemons   []*DaemonOptions     `json:"daemons"`
	P2P       *P2POptions          `json:"p2pManager"`
	Storage   *RedisOptions        `json:"storage"`
	Algorithm *AlgorithmOptions    `json:"algorithm"`
}

func (o *Options) TotalFeePercent() float64 {
	var totalFeePercent float64
	for i := range o.RewardRecipients {
		totalFeePercent = totalFeePercent + o.RewardRecipients[i].Percent
	}

	return totalFeePercent
}
