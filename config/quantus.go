package config

// QuantusOptions describes the node's external-miner endpoint (UDP/QUIC).
// Pool-facing miner certificates are configured with ports.<port>.tls.
type QuantusOptions struct {
	NodeAddress       string                 `json:"nodeAddress"`
	AuthTokenFile     string                 `json:"authTokenFile"`
	TLSCertSHA256File string                 `json:"tlsCertSHA256File"`
	Payments          *QuantusPaymentOptions `json:"payments,omitempty"`
}

// Amounts are decimal strings in base units (1 QTC = 10^12 base units).
// The signing wallet must be dedicated to this pool and funded separately from
// the node's wormhole mining-reward address.
type QuantusPaymentOptions struct {
	RPCURL          string `json:"rpcUrl"`
	GenesisHash     string `json:"genesisHash"`
	RewardAddress   string `json:"rewardAddress"`
	WalletScript    string `json:"walletScript"`
	SeedFile        string `json:"seedFile"`
	MinPaymentUnits string `json:"minPaymentUnits"`
	ReserveUnits    string `json:"reserveUnits"`
}
