package types

// Block is a basic type for db storage
type Block struct {
	Hash              string   `json:"hash"`
	Confirmations     int      `json:"confirmations"`
	StrippedSize      int      `json:"strippedsize"`
	Size              int      `json:"size"`
	Weight            int      `json:"weight"`
	Height            int      `json:"height"`
	Version           int      `json:"version"`
	VersionHex        string   `json:"versionHex"`
	MerkleRoot        string   `json:"merkleroot"`
	Tx                []string `json:"tx"`
	Time              int      `json:"time"`
	MedianTime        int      `json:"mediantime"`
	Nonce             int      `json:"nonce"`
	Bits              string   `json:"bits"`
	Difficulty        float64  `json:"difficulty"`
	ChainWork         string   `json:"chainwork"`
	NTx               int      `json:"nTx"`
	PreviousBlockHash string   `json:"previousblockhash"`
}
