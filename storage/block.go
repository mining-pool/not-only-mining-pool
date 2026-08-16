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
	Finder string // miner who submitted the block-solving share (solo payMode)
	Mark   int64  // pplns share-sequence at block time (window upper bound)
}

func (pb *PendingBlock) String() string {
	return strings.Join([]string{
		pb.Hash, pb.TxHash, strconv.FormatUint(pb.Height, 10), pb.Finder, strconv.FormatInt(pb.Mark, 10),
	}, ":")
}

// NewPendingBlockFromString parses a stored pending block. Finder and Mark are
// optional (older 3-field records parse fine) so the format can evolve.
func NewPendingBlockFromString(str string) (*PendingBlock, error) {
	split := strings.Split(str, ":")
	if len(split) < 3 {
		return nil, fmt.Errorf("pending block string %s lacks element(s)", str)
	}

	height, err := strconv.ParseUint(split[2], 10, 64)
	if err != nil {
		return nil, err
	}
	pb := &PendingBlock{Hash: split[0], TxHash: split[1], Height: height}
	if len(split) > 3 {
		pb.Finder = split[3]
	}
	if len(split) > 4 {
		pb.Mark, _ = strconv.ParseInt(split[4], 10, 64)
	}
	return pb, nil
}
