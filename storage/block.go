package storage

import (
	"fmt"
	"strconv"
	"strings"
)

type BlockCategory string

const (
	Immature BlockCategory = "immature"
	Send     BlockCategory = "send"
	Orphan   BlockCategory = "orphan"
	Generate BlockCategory = "generate"
	Receive  BlockCategory = "receive"
	Move     BlockCategory = "move"
	Kicked   BlockCategory = "kicked"
)

type PendingBlock struct {
	Hash   string
	TxHash string
	Height uint64
}

func (pb *PendingBlock) String() string {
	return pb.Hash + ":" + pb.TxHash + ":" + strconv.FormatUint(pb.Height, 10)
}

func NewPendingBlockFromString(str string) (*PendingBlock, error) {
	split := strings.Split(str, ":")
	if len(split) != 3 {
		return nil, fmt.Errorf("pending block string %s lacks element(s)", str)
	}

	hash := split[0]
	txHash := split[1]
	height, err := strconv.ParseUint(split[2], 10, 64)
	if err != nil {
		return nil, err
	}

	return &PendingBlock{
		Hash:   hash,
		TxHash: txHash,
		Height: height,
	}, nil
}
