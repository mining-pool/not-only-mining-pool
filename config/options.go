package config

import (
	"github.com/node-standalone-pool/go-pool-server/utils"
	"log"
	"strconv"
	"strings"
)

type BanningOptions struct {
	Enabled        bool    `json:"enabled"`
	Time           int     `json:"time"`
	InvalidPercent float64 `json:"invalidPercent"`
	CheckThreshold uint64  `json:"checkThreshold"`
	PurgeInterval  int     `json:"purgeInterval"` // unit seconds
}

type CoinOptions struct {
	Name                string `json:"name"`
	Symbol              string `json:"symbol"`
	Algorithm           string `json:"algorithm"`
	TxMessages          bool   `json:"txMessages"`
	Reward              string `json:"reward"`
	NoGetBlockchainInfo bool   `json:"noGetBlockchainInfo"`
	NoSubmitMethod      bool   `json:"noSubmitMethod"`
	PeerMagic           string `json:"peerMagic"`
}

type VarDiffOptions struct {
	MinDiff         float64 `json:"minDiff"`
	MaxDiff         float64 `json:"maxDiff"`
	TargetTime      int64   `json:"targetTime"`
	RetargetTime    int64   `json:"retargetTime"`
	VariancePercent float64 `json:"variancePercent"`
	X2Mode          bool    `json:"x2mode"`
}

type PortOptions struct {
	Diff    float64         `json:"diff"`
	VarDiff *VarDiffOptions `json:"varDiff"`
	TLS     bool            `json:"tls"`
}

type DaemonOptions struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	TLS      bool   `json:"tls"`
}

func (d *DaemonOptions) String() string {
	return d.User + ":" + d.Password + "@" + d.Host + strconv.FormatInt(int64(d.Port), 10)
}

func (d *DaemonOptions) URL() string {
	if d.TLS {
		return "https://" + d.Host + ":" + strconv.FormatInt(int64(d.Port), 10)
	}

	return "http://" + d.Host + ":" + strconv.FormatInt(int64(d.Port), 10)
}

type P2POptions struct {
	Enabled             bool   `json:"enabled"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	Magic               string `json:"magic"`
	DisableTransactions bool   `json:"disableTransactions"`
}

func (p2p *P2POptions) Addr() string {
	return p2p.Host + ":" + strconv.FormatInt(int64(p2p.Port), 10)
}

type Recipient struct {
	Address string  `json:"address"`
	Type    string  `json:"type"`
	Percent float64 `json:"percent"`

	script []byte
}

func (r *Recipient) GetScript() []byte {
	if r.script == nil {
		switch strings.ToLower(r.Type) {
		case "p2sh":
			r.script = utils.P2SHAddressToScript(r.Address)
		case "p2pkh":
			r.script = utils.P2SHAddressToScript(r.Address)
		case "pk", "publickey", "minerkey":
			r.script = utils.PublicKeyToScript(r.Address)
		case "":
			log.Fatal(r.Address + " has no type!")
		default:
			log.Fatal(r.Address + " uses an unsupported type: " + r.Type)

		}
	}

	return r.script
}

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
	P2P     *P2POptions          `json:"p2p"`

	NoSubmitMethod bool `json:"noSubmitMethod"`

	Testnet           bool   `json:"-"`
	PoolAddressScript []byte `json:"-"` // not recommend to input from config
	//ProtocolVersion        int                  `json:"-"`
	FeePercent float64 `json:"-"`
}
