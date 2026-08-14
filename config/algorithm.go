package config

type AlgorithmOptions struct {
	Name       string `json:"name"`
	Multiplier int    `json:"multiplier"`

	// SHA256dBlockHasher selects how a found block's hash (block id) is
	// computed when BlockHasher is empty: true -> sha256d(header) (LTC-style),
	// false -> the PoW algorithm itself (DASH-style).
	SHA256dBlockHasher bool `json:"sha256dBlockHasher"`

	// BlockHasher, when set, names the registered algorithm used for the block
	// id explicitly and overrides SHA256dBlockHasher. e.g. groestlcoin mines
	// with "groestl" but identifies blocks by a SINGLE "sha256".
	BlockHasher string `json:"blockHasher"`

	// CoinbaseHasher names the algorithm used to hash the coinbase into its txid
	// and to fold the transaction merkle tree. Empty defaults to "sha256d".
	// Groestlcoin uses a single "sha256" here.
	CoinbaseHasher string `json:"coinbaseHasher"`
}
