package config

type CoinOptions struct {
	Name       string `json:"name"`
	Symbol     string `json:"symbol"`
	TxMessages bool   `json:"txMessages"`

	// GBTRules are the getblocktemplate rule sets. Empty defaults to ["segwit"].
	// Litecoin requires ["mweb","segwit"]; some coins need [] (no rules).
	GBTRules []string `json:"gbtRules"`

	// auto-filled from rpc
	Reward        string `json:"reward"`
	NoSubmitBlock bool   `json:"noSubmitBlock"`
	Testnet       bool   `json:"testnet"`
}
