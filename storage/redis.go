package storage

import (
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v7"
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
	}

	result, err := client.Ping().Result()
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
	if share.ErrorCode == 0 {
		ppl.HIncrByFloat(s.coin+":shares:roundCount", share.Miner, share.Diff)
		ppl.HIncrBy(s.coin+":stats", "validShares", 1)
		ppl.ZAdd(s.coin+":hashrate", &redis.Z{
			Score: float64(now),
			Member: strings.Join([]string{
				strconv.FormatFloat(share.Diff, 'f', 5, 64),
				share.Miner,
				strNow,
			}, ":"),
		})
	} else {
		ppl.HIncrBy(s.coin+":stats", "invalidShares", 1)
	}

	// when mined one => seal roundCount,
	// BlockHex is not accuracy, maybe out of date
	if len(share.BlockHex) > 0 {
		if accepted {
			ppl.Rename(s.coin+":shares:roundCount", s.coin+":shares:round"+strconv.FormatInt(share.BlockHeight, 10))
			//ppl.Rename(s.coin+":shares:timesCount", strings.Join([]string{
			//	s.coin,
			//	"shares:round",
			//	strconv.FormatInt(share.BlockHeight, 10),
			//}, ":"))
			ppl.SAdd(s.coin+":blocksPending", strings.Join([]string{
				s.coin,
				share.BlockHash,
				share.TxHash,
				strconv.FormatInt(share.BlockHeight, 10),
				share.Miner,
				strNow,
			}, ":"))
			ppl.HIncrBy(s.coin+":stats", "validBlocks", 1)
		} else {
			ppl.HIncrBy(s.coin+":stats", "invalidBlocks", 1)
		}
	}

	_, err := ppl.Exec()
	if err != nil {
		log.Error(err)
	}
}

func (s *DB) PutPendingBlockHash(blockHash string) {
	s.Client.SAdd(s.coin+":stats:blockPending", blockHash)
}

// TODO
func (s *DB) GetShares() []*types.Share {
	// s.Client.Z
	return nil
}

func (s *DB) GetStats() {
}

//             ["scard", ":blocksPending"],
//            ["scard", ":blocksConfirmed"],
//            ["scard", ":blocksKicked"]
