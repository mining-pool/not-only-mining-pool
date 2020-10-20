package daemons

import (
	"encoding/json"
	"fmt"
)

type GetBlockchainInfo struct {
	Chain                string      `json:"chain"`
	Blocks               int         `json:"blocks"`
	Headers              int         `json:"headers"`
	Bestblockhash        string      `json:"bestblockhash"`
	Difficulty           float64     `json:"difficulty"`
	Mediantime           int         `json:"mediantime"`
	Verificationprogress float64     `json:"verificationprogress"`
	Chainwork            string      `json:"chainwork"`
	Pruned               bool        `json:"pruned"`
	Softforks            interface{} // having difference between LTC & BTC, so use interface to suppress the error
	Bip9Softforks        struct {
		Csv struct {
			Status    string `json:"status"`
			StartTime int    `json:"startTime"`
			Timeout   int    `json:"timeout"`
			Since     int    `json:"since"`
		} `json:"csv"`
		Dip0001 struct {
			Status    string `json:"status"`
			StartTime int    `json:"startTime"`
			Timeout   int    `json:"timeout"`
			Since     int    `json:"since"`
		} `json:"dip0001"`
		Dip0003 struct {
			Status    string `json:"status"`
			StartTime int    `json:"startTime"`
			Timeout   int    `json:"timeout"`
			Since     int    `json:"since"`
		} `json:"dip0003"`
		Dip0008 struct {
			Status    string `json:"status"`
			StartTime int    `json:"startTime"`
			Timeout   int    `json:"timeout"`
			Since     int    `json:"since"`
		} `json:"dip0008"`
		Bip147 struct {
			Status    string `json:"status"`
			StartTime int    `json:"startTime"`
			Timeout   int    `json:"timeout"`
			Since     int    `json:"since"`
		} `json:"bip147"`
	} `json:"bip9_softforks"`
}

func BytesToGetBlockchainInfo(b []byte) *GetBlockchainInfo {
	var getBlockchainInfo GetBlockchainInfo
	err := json.Unmarshal(b, &getBlockchainInfo)
	if err != nil {
		log.Panic(fmt.Sprint("getblockchaininfo call failed with error: ", err))
	}

	return &getBlockchainInfo
}
