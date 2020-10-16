package storage

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	logging "github.com/ipfs/go-log/v2"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/types"
)

var log = logging.Logger("storage")

type DB struct {
	*redis.Client
	coin string
}

func NewStorage(coinName string, options *config.RedisOptions) *DB {
	client := redis.NewClient(options.ToRedisOptions())
	if client == nil {
		log.Panic("failed to connect to the redis server. If you dont wanna db storage please delete redis config in config file")
		return nil
	}

	result, err := client.Ping(context.Background()).Result()
	if err != nil || strings.ToLower(result) != "pong" {
		log.Panicf("failed to connect to the redis server: %s %s", result, err)
	}

	return &DB{
		Client: client,
		coin:   coinName,
	}
}

func (s *DB) PutShare(share *types.Share, accepted bool) {
	now := time.Now().Unix()
	strNow := strconv.FormatInt(now, 10)

	ppl := s.Pipeline()
	ctx := context.Background()

	strDiff := strconv.FormatFloat(share.Diff, 'f', 5, 64)
	ppl.SAdd(ctx, s.coin+":pool:miners", share.Miner)              // miner index
	ppl.SAdd(ctx, s.coin+":miner:"+share.Miner+":rigs", share.Rig) // rig index

	if share.ErrorCode == 0 {
		ppl.HIncrByFloat(ctx, s.coin+":miner:contrib", share.Miner, share.Diff)
		ppl.HIncrBy(ctx, s.coin+":miner:shares:valid", share.Miner, 1)

		ppl.HIncrBy(ctx, s.coin+":pool", "validShares", 1)

		ppl.ZAdd(ctx, s.coin+":pool:shares", &redis.Z{
			Score:  float64(now),
			Member: strDiff,
		})

		ppl.ZAdd(ctx, s.coin+":miner:"+share.Miner+":hashes", &redis.Z{
			Score:  float64(now),
			Member: strDiff,
		})

		ppl.ZAdd(ctx, s.coin+":miner:"+share.Miner+":rig:"+share.Rig+":hashes", &redis.Z{
			Score:  float64(now),
			Member: strDiff,
		})

	} else {
		ppl.HIncrBy(ctx, s.coin+":miner:shares:invalid", share.Miner, 1)
		ppl.IncrBy(ctx, s.coin+":pool:shares:invalid", 1)
	}

	// when mined one => seal roundCount,
	// BlockHex is not accuracy, maybe out of date
	if len(share.BlockHex) > 0 {
		if accepted {
			ppl.Rename(ctx, s.coin+":shares:round", s.coin+":shares:round:"+strconv.FormatInt(share.BlockHeight, 10))
			ppl.SAdd(ctx, s.coin+":blocks:pending", share.BlockHash)
			ppl.HSetNX(ctx, s.coin+":blocks", share.BlockHash, strings.Join([]string{
				share.TxHash,
				strconv.FormatInt(share.BlockHeight, 10),
				share.Miner,
				strNow,
			}, ":"))

			ppl.HIncrBy(ctx, s.coin+":pool", "validBlocks", 1)
		} else {
			ppl.HIncrBy(ctx, s.coin+":pool", "invalidBlocks", 1)
		}
	}

	_, err := ppl.Exec(ctx)
	if err != nil {
		log.Error(err)
	}
}

func (s *DB) GetMinerIndex() ([]string, error) {
	return s.SMembers(context.Background(), s.coin+":pool:miners").Result()
}

func (s *DB) GetRigIndex(minerName string) ([]string, error) {
	return s.SMembers(context.Background(), s.coin+":miner:"+minerName+":rigs").Result()
}

// GetCurrentRoundCount will return a total diff of shares the miner submitted
func (s *DB) GetMinerCurrentRoundContrib(minerName string) (float64, error) {
	return s.HGet(context.Background(), s.coin+":shares:contrib", minerName).Float64()
}

// GetMinerTotalShares will return the number of all valid shares
func (s *DB) GetPoolTotalValidShares() (uint64, error) {
	return s.HGet(context.Background(), s.coin+":pool", "validShares").Uint64()
}

// GetMinerTotalShares will return the number of all valid blocks
func (s *DB) GetPoolTotalValidBlocks() (uint64, error) {
	return s.HGet(context.Background(), s.coin+":pool", "validBlocks").Uint64()
}

// GetMinerTotalShares will return the number of all invalid shares
func (s *DB) GetPoolTotalInvalidShares() (uint64, error) {
	return s.HGet(context.Background(), s.coin+":pool", "validShares").Uint64()
}

// GetMinerTotalShares will return the number of all invalid blocks
func (s *DB) GetPoolTotalInvalidBlocks() (uint64, error) {
	return s.HGet(context.Background(), s.coin+":pool", "invalidBlocks").Uint64()
}

// GetMinerTotalShares will return the number of all invalid blocks
func (s *DB) GetRigHashrate(minerName, rigName string, from, to int64) (hashrate float64, err error) {
	slice, err := s.ZRange(context.Background(), s.coin+":miner:"+minerName+":rig:"+rigName+":hashes", from, to).Result()
	if err != nil {
		return 0.0, err
	}

	var totalDiff float64
	for i := range slice {
		diff, err := strconv.ParseFloat(slice[i], 64)
		if err != nil {
			return 0.0, err
		}

		totalDiff += diff
	}

	return totalDiff / float64(to-from), nil
}

// GetMinerTotalShares will return the number of all invalid blocks
func (s *DB) GetMinerHashrate(minerName string, from, to int64) (hashrate float64, err error) {
	slice, err := s.ZRange(context.Background(), s.coin+":miner:"+minerName+":shares", from, to).Result()
	if err != nil {
		return 0.0, err
	}

	var totalDiff float64
	for i := range slice {
		diff, err := strconv.ParseFloat(slice[i], 64)
		if err != nil {
			return 0.0, err
		}

		totalDiff += diff
	}

	return totalDiff / float64(to-from), nil
}

// GetMinerTotalShares will return the number of all invalid blocks
func (s *DB) GetPoolHashrate(from, to int64) (float64, error) {
	slice, err := s.ZRange(context.Background(), s.coin+":pool:shares", from, to).Result()
	if err != nil {
		return 0.0, err
	}

	var totalDiff float64
	for i := range slice {
		diff, err := strconv.ParseFloat(slice[i], 64)
		if err != nil {
			return 0.0, err
		}

		totalDiff += diff
	}

	return totalDiff / float64(to-from), nil
}

// GetCurrentRoundCount will return a total diff of shares the miner submitted
func (s *DB) GetMinerRigs(minerName string) (float64, error) {
	return s.HGet(context.Background(), s.coin+":shares:contrib", minerName).Float64()
}

// ConfirmBlock alt one pending block to confirmed
func (s *DB) ConfirmBlock(blockHash string) (ok bool, err error) {
	return s.SMove(context.Background(), s.coin+":blocks:pending", s.coin+":blocks:confirmed", blockHash).Result()
}

// KickBlock alt one pending block to kicked
func (s *DB) KickBlock(blockHash string) (ok bool, err error) {
	return s.SMove(context.Background(), s.coin+":blocks:pending", s.coin+":blocks:kicked", blockHash).Result()
}
