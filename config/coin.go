package config

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
