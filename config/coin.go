package config

type CoinOptions struct {
	Name                string `json:"name"`
	Symbol              string `json:"symbol"`
	TxMessages          bool   `json:"txMessages"`
	Reward              string `json:"reward"`
	NoGetBlockchainInfo bool   `json:"noGetBlockchainInfo"`
	NoSubmitBlock       bool   `json:"noSubmitBlock"`
}
