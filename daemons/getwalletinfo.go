package daemons

import (
	"encoding/json"
	"fmt"
)

type GetWalletInfo struct {
	Walletversion      int     `json:"walletversion"`
	Balance            float64 `json:"balance"`
	PrivatesendBalance float64 `json:"privatesend_balance"`
	UnconfirmedBalance float64 `json:"unconfirmed_balance"`
	ImmatureBalance    float64 `json:"immature_balance"`
	Txcount            int     `json:"txcount"`
	Keypoololdest      int     `json:"keypoololdest"`
	Keypoolsize        int     `json:"keypoolsize"`
	KeysLeft           int     `json:"keys_left"`
	Paytxfee           float64 `json:"paytxfee"`
}

func BytesToGetWalletInfo(b []byte) *GetWalletInfo {
	var getWalletInfo GetWalletInfo
	err := json.Unmarshal(b, &getWalletInfo)
	if err != nil {
		log.Fatal(fmt.Sprint("getDifficulty call failed with error ", err))
	}

	return &getWalletInfo
}
