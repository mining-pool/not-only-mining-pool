package config

import "strconv"

type P2POptions struct {
	Host                string `json:"host"`
	Port                int    `json:"port"`
	Magic               string `json:"magic"`
	DisableTransactions bool   `json:"disableTransactions"`
}

func (p2p *P2POptions) Addr() string {
	return p2p.Host + ":" + strconv.FormatInt(int64(p2p.Port), 10)
}
