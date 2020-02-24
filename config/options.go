package config

type Options struct {
	Coin *CoinOptions `json:"coin"`

	PoolAddress      *Recipient   `json:"poolAddress"`
	RewardRecipients []*Recipient `json:"rewardRecipients"`

	BlockRefreshInterval   int  `json:"blockRefreshInterval"`
	JobRebroadcastTimeout  int  `json:"jobRebroadcastTimeout"`
	ConnectionTimeout      int  `json:"connectionTimeout"`
	EmitInvalidBlockHashes bool `json:"emitInvalidBlockHashes"`
	TCPProxyProtocol       bool `json:"tcpProxyProtocol"` // http://www.haproxy.org/download/1.8/doc/proxy-protocol.txt

	Banning *BanningOptions      `json:"banning"`
	Ports   map[int]*PortOptions `json:"ports"`
	Daemons []*DaemonOptions     `json:"daemons"`
	P2P     *P2POptions          `json:"p2pManager"`

	NoSubmitMethod bool `json:"noSubmitMethod"`

	Testnet           bool   `json:"-"`
	PoolAddressScript []byte `json:"-"` // not recommend to input from config
}

func (o *Options) TotalFeePercent() float64 {
	var totalFeePercent float64
	for i := range o.RewardRecipients {
		totalFeePercent = totalFeePercent + o.RewardRecipients[i].Percent
	}

	return totalFeePercent
}
