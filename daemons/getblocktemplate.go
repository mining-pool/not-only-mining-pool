package daemons

import (
	"encoding/json"
	"errors"
	"fmt"
)

type MasternodeParams struct {
	Payee  string `json:"payee"`
	Script string `json:"script"`
	Amount uint64 `json:"amount"`
}

type SuperblockParams struct {
	Payee  string `json:"payee"`
	Script string `json:"script"`
	Amount uint64 `json:"amount"`
}

type TxParams struct {
	Data    string        `json:"data"`
	Hash    string        `json:"hash"`
	Depends []interface{} `json:"depends"`
	Fee     uint64        `json:"fee"`
	Sigops  int           `json:"sigops"`
	TxId    string        `json:"txid"`
}

type GetBlockTemplate struct {
	// Base fields from BIP 0022.  CoinbaseAux is optional.  One of
	// CoinbaseTxn or CoinbaseValue must be specified, but not both.
	Version int32  `json:"version"`
	Bits    string `json:"bits"`
	CurTime uint32 `json:"curtime"`
	Height  int64  `json:"height"`
	// Rules             []string    `json:"rules"`
	PreviousBlockHash string `json:"previousblockhash"`
	// PreviousBits      string      `json:"previousbits"`
	// SigOpLimit        int64       `json:"sigoplimit,omitempty"`
	// SizeLimit         int64       `json:"sizelimit,omitempty"`
	// WeightLimit       int64       `json:"weightlimit,omitempty"`
	// WorkID            string      `json:"workid,omitempty"`
	Transactions []*TxParams `json:"transactions"`
	// CoinbaseTxn       *TxParams   `json:"coinbasetxn,omitempty"` // Bitcoin does not produce the coinbasetxn for you, you will have to build it manually.
	CoinbaseAux struct {
		Flags string `json:"flags"`
	} `json:"coinbaseaux"`
	CoinbaseValue uint64 `json:"coinbasevalue"`

	// Block proposal from BIP 0023.
	// Capabilities []string `json:"capabilities,omitempty"`
	// RejectReason string   `json:"reject-reason,omitempty"`

	//Vbavailable struct {
	//} `json:"vbavailable"`
	//Vbrequired int `json:"vbrequired"`

	// Witness commitment defined in BIP 0141.
	DefaultWitnessCommitment string `json:"default_witness_commitment,omitempty"`

	// Optional long polling from BIP 0022.
	// LongPollID  string `json:"longpollid,omitempty"`
	// LongPollURI string `json:"longpolluri,omitempty"`
	// SubmitOld   *bool  `json:"submitold,omitempty"`

	// Basic pool extension from BIP 0023.
	Target string `json:"target,omitempty"`
	// Expires int64  `json:"expires,omitempty"`

	// Mutations from BIP 0023
	// MaxTime    int64    `json:"maxtime,omitempty"`
	// MinTime    int64    `json:"mintime,omitempty"`
	// Mutable    []string `json:"mutable,omitempty"`
	// NonceRange string   `json:"noncerange,omitempty"`

	// Dash
	Masternode []MasternodeParams `json:"masternode"`
	// MasternodePaymentsStarted  bool               `json:"masternode_payments_started"`
	// MasternodePaymentsEnforced bool               `json:"masternode_payments_enforced"`

	Superblock []SuperblockParams `json:"superblock"`
	// SuperblocksStarted bool               `json:"superblocks_started"`
	// SuperblocksEnabled bool               `json:"superblocks_enabled"`
	CoinbasePayload string `json:"coinbase_payload"`

	// unknown
	Votes              []string
	MasternodePayments interface{}
	Payee              interface{}
	PayeeAmount        interface{}
}

// then JobManager.ProcessTemplate(rpcData)
func (dm *DaemonManager) GetBlockTemplate() (getBlockTemplate *GetBlockTemplate, err error) {
	instance, result, _ := dm.Cmd("getblocktemplate",
		[]interface{}{map[string]interface{}{"capabilities": []string{"coinbasetxn", "workid", "coinbase/append"}, "rules": []string{"segwit"}}},
	)

	if result.Error != nil {
		return nil, errors.New(fmt.Sprint("getblocktemplate call failed for daemon instance ", instance, " with error ", result.Error))
	}

	getBlockTemplate = BytesToGetBlockTemplate(result.Result)
	if getBlockTemplate == nil {
		return nil, errors.New(fmt.Sprint("getblocktemplate call failed for daemon instance ", instance, " with error ", getBlockTemplate))
	}

	return getBlockTemplate, nil
}

func BytesToGetBlockTemplate(b []byte) *GetBlockTemplate {
	var getBlockTemplate GetBlockTemplate
	err := json.Unmarshal(b, &getBlockTemplate)
	if err != nil {
		log.Fatal(fmt.Sprint("getblocktemplate call failed with error ", err))
	}

	return &getBlockTemplate
}
