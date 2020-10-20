package daemons

import (
	"encoding/json"
	"fmt"
)

type GetInfo struct {
	Version            int     `json:"version"`
	Protocolversion    int     `json:"protocolversion"`
	Walletversion      int     `json:"walletversion"`
	Balance            float64 `json:"balance"`
	PrivatesendBalance float64 `json:"privatesend_balance"`
	Blocks             int     `json:"blocks"`
	Timeoffset         int     `json:"timeoffset"`
	Connections        int     `json:"connections"`
	Proxy              string  `json:"proxy"`
	Difficulty         float64 `json:"difficulty"`
	Testnet            bool    `json:"testnet"`
	Keypoololdest      int     `json:"keypoololdest"`
	Keypoolsize        int     `json:"keypoolsize"`
	Paytxfee           float64 `json:"paytxfee"`
	Relayfee           float64 `json:"relayfee"`
	Errors             string  `json:"errors"`
}

func BytesToGetInfo(b []byte) *GetInfo {
	var getInfo GetInfo
	err := json.Unmarshal(b, &getInfo)
	if err != nil {
		log.Fatal(fmt.Sprint("getDifficulty call failed with error ", err))
	}

	return &getInfo
}
