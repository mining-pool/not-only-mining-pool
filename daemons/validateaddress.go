package daemons

import (
	"encoding/json"
	"fmt"
)

type ValidateAddress struct {
	Isvalid      bool   `json:"isvalid"`
	Address      string `json:"address"`
	ScriptPubKey string `json:"scriptPubKey"`
	IsMine       bool   `json:"ismine"`
	Iswatchonly  bool   `json:"iswatchonly"`
	Isscript     bool   `json:"isscript"`
	Iswitness    bool   `json:"iswitness"`
	Script       string `json:"script"`
	Hex          string `json:"hex"`
	Pubkey       string `json:"pubkey"`
	Embedded     struct {
		Isscript       bool   `json:"isscript"`
		Iswitness      bool   `json:"iswitness"`
		WitnessVersion int    `json:"witness_version"`
		WitnessProgram string `json:"witness_program"`
		Pubkey         string `json:"pubkey"`
		Address        string `json:"address"`
		ScriptPubKey   string `json:"scriptPubKey"`
	} `json:"embedded"`
	Addresses     []string `json:"addresses"`
	Label         string   `json:"label"`
	Timestamp     int      `json:"timestamp"`
	Hdkeypath     string   `json:"hdkeypath"`
	Hdseedid      string   `json:"hdseedid"`
	Hdmasterkeyid string   `json:"hdmasterkeyid"`
	Labels        []struct {
		Name    string `json:"name"`
		Purpose string `json:"purpose"`
	} `json:"labels"`
}

func BytesToValidateAddress(b []byte) (*ValidateAddress, error) {
	var validateAddress ValidateAddress
	err := json.Unmarshal(b, &validateAddress)
	if err != nil {
		return nil, fmt.Errorf("unmashal validateAddress response %s failed with error %s", b, err)
	}

	return &validateAddress, nil
}
