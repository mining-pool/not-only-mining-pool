package types

import (
	"net"
)

type Share struct {
	JobId       string    `json:"jobId"`
	RemoteAddr  net.Addr  `json:"remoteAddr"`
	Miner       string    `json:"miner"`
	Rig         string    `json:"rig"`
	ErrorCode   ErrorWrap `json:"errorCode"`
	BlockHeight int64     `json:"height"`
	BlockReward uint64    `json:"blockReward"`
	Diff        float64   `json:"shareDiff"`
	BlockHash   string    `json:"blockHash"`
	BlockHex    string    `json:"blockHex"`
	TxHash      string    `json:"txHash"`
}
