package daemons

import (
	"encoding/json"
	"fmt"
)

type GetTransaction struct {
	Amount            float64                `json:"amount"`
	Confirmations     int                    `json:"confirmations"`
	Generated         bool                   `json:"generated"`
	Blockhash         string                 `json:"blockhash"`
	Blockheight       int                    `json:"blockheight"`
	Blockindex        int                    `json:"blockindex"`
	Blocktime         int                    `json:"blocktime"`
	Txid              string                 `json:"txid"`
	Walletconflicts   []interface{}          `json:"walletconflicts"`
	Time              int                    `json:"time"`
	Timereceived      int                    `json:"timereceived"`
	Bip125Replaceable string                 `json:"bip125-replaceable"`
	Details           []GetTransactionDetail `json:"details"`
	Hex               string                 `json:"hex"`
}

type GetTransactionDetail struct {
	Address  string  `json:"address"`
	Category string  `json:"category"`
	Amount   float64 `json:"amount"`
	Label    string  `json:"label"`
	Vout     int     `json:"vout"`
}

func BytesToGetTransaction(b []byte) (*GetTransaction, error) {
	var getTransaction GetTransaction
	err := json.Unmarshal(b, &getTransaction)
	if err != nil {
		return nil, fmt.Errorf("getTransaction call failed with error %s", err)
	}

	return &getTransaction, nil
}
