package config

type CoinOptions struct {
	Name       string `json:"name"`
	Symbol     string `json:"symbol"`
	TxMessages bool   `json:"txMessages"`

	// auto-filled from rpc
	Reward        string `json:"reward"`
	NoSubmitBlock bool   `json:"noSubmitBlock"`
	Testnet       bool   `json:"testnet"`
}
