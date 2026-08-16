package storage

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"
	logging "github.com/ipfs/go-log/v2"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/types"
)

var log = logging.Logger("storage")

// pplnsLogCap bounds the PPLNS share log (a ZSet of recent shares) so it can
// never grow without limit; the oldest entries are trimmed as new ones arrive.
// Atomic so tests can shrink it without racing the writer goroutine. NOTE: this
// rank cap is applied only outside PPS mode — see putShareNow — because it would
// otherwise drop shares the PPS cursor hasn't credited yet (underpayment). In PPS
// the log is trimmed by the cursor instead (ApplyPayments), retaining exactly the
// uncredited shares.
var pplnsLogCap atomic.Int64

func init() { pplnsLogCap.Store(200000) }

type DB struct {
	*redis.Client
	coin    string
	shares  chan shareJob
	ppsMode bool // pps retains uncredited shares (trimmed by cursor, not by rank)
}

// SetPPSMode switches the PPLNS-log retention policy: in PPS mode the log is not
// rank-capped (that could drop uncredited shares); it is trimmed by the payout
// cursor instead. Set once at startup, before shares flow.
func (s *DB) SetPPSMode(pps bool) { s.ppsMode = pps }

type shareJob struct {
	share    *types.Share
	accepted bool
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

	db := &DB{
		Client: client,
		coin:   coinName,
		shares: make(chan shareJob, 4096),
	}
	go db.shareWriter()
	return db
}

// PutShare enqueues a share for the single ordered writer and returns at once, so
// a submitting client goroutine never blocks on redis. Ordering matters: shares
// are applied FIFO so a block's round seal (Rename roundCurrent -> round<height>)
// captures exactly the shares recorded before it — a per-share goroutine let the
// seal race the round-contribution increments and mis-attribute rounds.
func (s *DB) PutShare(share *types.Share, accepted bool) {
	s.shares <- shareJob{share: share, accepted: accepted}
}

// shareWriter drains the queue and applies each share in submission order.
func (s *DB) shareWriter() {
	for job := range s.shares {
		s.putShareNow(job.share, job.accepted)
	}
}

func (s *DB) putShareNow(share *types.Share, accepted bool) {
	now := time.Now().Unix()

	ppl := s.Pipeline()
	ctx := context.Background()

	strDiff := strconv.FormatFloat(share.Diff, 'f', 5, 64)
	ppl.SAdd(ctx, s.coin+":pool:miners", share.Miner)              // miner index
	ppl.SAdd(ctx, s.coin+":miner:"+share.Miner+":rigs", share.Rig) // rig index

	var seq int64
	if share.ErrorCode == 0 {
		log.Info("recording valid share")
		// PPLNS log: append this share to a capped, monotonically-scored ZSet so a
		// block can pay the last-N-difficulty window across rounds. seq is fetched
		// synchronously so it can also mark the block's window upper bound.
		seq, _ = s.Incr(ctx, s.coin+":shares:seq").Result()
		ppl.ZAdd(ctx, s.coin+":shares:pplnslog", &redis.Z{
			Score:  float64(seq),
			Member: share.Miner + ":" + strconv.FormatFloat(share.Diff, 'f', -1, 64) + ":" + strconv.FormatInt(seq, 10),
		})
		// Rank-cap the log so it can't grow unbounded — but ONLY outside PPS mode.
		// In PPS the log is trimmed by the payout cursor (ApplyPayments), so a burst
		// of >pplnsLogCap shares between runs never drops uncredited ones.
		if !s.ppsMode {
			ppl.ZRemRangeByRank(ctx, s.coin+":shares:pplnslog", 0, -(pplnsLogCap.Load() + 1))
		}
		// current-round contribution, sealed to shares:round<height> on a block.
		ppl.HIncrByFloat(ctx, s.coin+":shares:roundCurrent", share.Miner, share.Diff)
		ppl.HIncrBy(ctx, s.coin+":miners:validShares", share.Miner, 1)

		ppl.HIncrBy(ctx, s.coin+":pool", "validShares", 1)

		// cost storage for speed, dont use for range to replace this
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
		log.Warn("recording invalid share")
		ppl.HIncrBy(ctx, s.coin+":miners:invalidShares", share.Miner, 1)

		ppl.HIncrBy(ctx, s.coin+":pool", "invalidShares", 1)
	}

	// when mined one => seal roundCount,
	// BlockHex is not accuracy, maybe out of date
	if len(share.BlockHex) > 0 {
		// share is valid but block from share can be also invalid
		if accepted {
			log.Warn("recording valid block")
			// Seal the current round to shares:round<height> so the payer can pay
			// exactly the miners who contributed to this block, and record the
			// block as pending with the data the payer needs: hash (block id),
			// txHash (coinbase, for gettransaction) and height (the round key).
			ppl.Rename(ctx, s.coin+":shares:roundCurrent", s.coin+":shares:round"+strconv.FormatInt(share.BlockHeight, 10))
			ppl.SAdd(ctx, s.coin+":blocks:pending", (&PendingBlock{
				Hash:   share.BlockHash,
				TxHash: share.TxHash,
				Height: uint64(share.BlockHeight),
				Finder: share.Miner, // solo payMode
				Mark:   seq,         // pplns window upper bound
			}).String())

			ppl.HIncrBy(ctx, s.coin+":pool", "validBlocks", 1)
		} else {
			log.Warn("recording invalid block")
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
	return s.HGet(context.Background(), s.coin+":shares:roundCurrent", minerName).Float64()
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

// PaymentUpdate is the atomic result of one payout run, applied by ApplyPayments
// in a single redis pipeline so balances, payouts and block state never diverge.
type PaymentUpdate struct {
	Balances     map[string]float64 // miner -> new carried-over balance (coin), absolute
	Paid         map[string]float64 // miner -> amount paid this run (coin), added to payouts
	Confirmed    []string           // pending block strings moving to confirmed
	Orphaned     []string           // pending block strings moving to orphaned
	DeleteRounds []uint64           // sealed round heights whose shares are now accounted
	PPSCursor    int64              // pps: advance the credited-share cursor (0 = leave)
}

// ApplyPayments persists one payout run atomically: it overwrites each miner's
// carried-over balance, adds this run's payouts, moves settled blocks out of the
// pending set, and drops the sealed rounds that were paid.
func (s *DB) ApplyPayments(u *PaymentUpdate) error {
	ctx := context.Background()
	// TxPipeline wraps the writes in MULTI/EXEC so a payout run's balance, payout,
	// block-state, round-deletion and cursor updates apply all-or-nothing.
	ppl := s.TxPipeline()
	for miner, bal := range u.Balances {
		ppl.HSet(ctx, s.coin+":balances", miner, strconv.FormatFloat(bal, 'f', -1, 64))
	}
	for miner, paid := range u.Paid {
		ppl.HIncrByFloat(ctx, s.coin+":payouts", miner, paid)
	}
	for _, b := range u.Confirmed {
		ppl.SMove(ctx, s.coin+":blocks:pending", s.coin+":blocks:confirmed", b)
	}
	for _, b := range u.Orphaned {
		ppl.SMove(ctx, s.coin+":blocks:pending", s.coin+":blocks:orphaned", b)
	}
	for _, h := range u.DeleteRounds {
		ppl.Del(ctx, s.coin+":shares:round"+strconv.FormatUint(h, 10))
	}
	if u.PPSCursor > 0 {
		ppl.Set(ctx, s.coin+":pps:cursor", u.PPSCursor, 0)
		// Shares up to the cursor are now credited — drop them so the PPS log stays
		// bounded by the uncredited tail instead of the (skipped) rank cap.
		ppl.ZRemRangeByScore(ctx, s.coin+":shares:pplnslog", "0", strconv.FormatInt(u.PPSCursor, 10))
	}
	// This run's intent is now realized — drop it in the same atomic commit so a
	// resumed run can't re-apply it.
	ppl.Del(ctx, s.coin+":payouts:intent")
	_, err := ppl.Exec(ctx)
	return err
}

// --- durable payout intent (double-spend guard) ---
//
// sendmany broadcasts on-chain, then ApplyPayments records it in redis. If the
// process dies between the two, a naive re-run would re-attribute the same blocks
// and pay again. To prevent that, settle writes the intended PaymentUpdate here
// BEFORE broadcasting and stamps the txid immediately AFTER; on the next run
// reconcile() finishes (if a txid is present) or halts for review (if not).

// PutPayoutIntent records the update a payout run is about to broadcast (txid
// empty until the send returns).
func (s *DB) PutPayoutIntent(u *PaymentUpdate) error {
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}
	return s.HSet(context.Background(), s.coin+":payouts:intent", "update", b, "txid", "").Err()
}

// SetPayoutIntentTxid stamps the broadcast transaction id onto the current intent
// as soon as sendmany returns, narrowing the ambiguous window to the RPC itself.
func (s *DB) SetPayoutIntentTxid(txid string) error {
	return s.HSet(context.Background(), s.coin+":payouts:intent", "txid", txid).Err()
}

// GetPayoutIntent returns the in-flight payout intent, if any.
func (s *DB) GetPayoutIntent() (update *PaymentUpdate, txid string, exists bool, err error) {
	m, err := s.HGetAll(context.Background(), s.coin+":payouts:intent").Result()
	if err != nil {
		return nil, "", false, err
	}
	if len(m) == 0 {
		return nil, "", false, nil
	}
	var u PaymentUpdate
	if err := json.Unmarshal([]byte(m["update"]), &u); err != nil {
		return nil, "", false, err
	}
	return &u, m["txid"], true, nil
}

// DelPayoutIntent clears the intent (used when a send is known not to have
// broadcast — e.g. the node rejected it).
func (s *DB) DelPayoutIntent() error {
	return s.Del(context.Background(), s.coin+":payouts:intent").Err()
}

func (s *DB) GetAllMinerBalances() (map[string]float64, error) {
	ss, err := s.HGetAll(context.Background(), s.coin+":balances").Result()
	if err != nil {
		return nil, err
	}
	balances := make(map[string]float64)
	for minerName, strBalance := range ss {
		balance, err := strconv.ParseFloat(strBalance, 64)
		if err != nil {
			return nil, err
		}
		balances[minerName] = balance
	}

	return balances, nil
}

func (s *DB) GetAllPendingBlocks() ([]*PendingBlock, error) {
	strBlocks, err := s.SMembers(context.Background(), s.coin+":blocks:pending").Result()
	if err != nil {
		return nil, err
	}

	blocks := make([]*PendingBlock, 0, len(strBlocks))
	for i := range strBlocks {
		block, err := NewPendingBlockFromString(strBlocks[i])
		if err != nil {
			return nil, err
		}

		blocks = append(blocks, block)
	}

	return blocks, nil
}

// GetPPLNSShares returns per-miner share difficulty over the last-N window
// ending at uptoSeq: it walks the PPLNS log backward from that mark, summing
// difficulty per miner until the cumulative difficulty reaches window (window<=0
// means no cap, bounded only by the log's own size).
func (s *DB) GetPPLNSShares(uptoSeq int64, window float64) (map[string]float64, error) {
	members, err := s.ZRevRangeByScore(context.Background(), s.coin+":shares:pplnslog", &redis.ZRangeBy{
		Min: "-inf",
		Max: strconv.FormatInt(uptoSeq, 10),
	}).Result()
	if err != nil {
		return nil, err
	}

	out := make(map[string]float64)
	var cum float64
	for _, m := range members {
		p := strings.SplitN(m, ":", 3) // miner:diff:seq
		if len(p) < 2 {
			continue
		}
		diff, err := strconv.ParseFloat(p[1], 64)
		if err != nil {
			continue
		}
		out[p[0]] += diff
		cum += diff
		if window > 0 && cum >= window {
			break
		}
	}
	return out, nil
}

// GetPPSCursor returns the highest share sequence already credited in pps mode.
func (s *DB) GetPPSCursor() (int64, error) {
	v, err := s.Get(context.Background(), s.coin+":pps:cursor").Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return v, err
}

// GetSharesSince returns per-miner share difficulty for every logged share newer
// than cursor, plus the highest sequence seen (so pps can advance the cursor).
func (s *DB) GetSharesSince(cursor int64) (map[string]float64, int64, error) {
	zs, err := s.ZRangeByScoreWithScores(context.Background(), s.coin+":shares:pplnslog", &redis.ZRangeBy{
		Min: "(" + strconv.FormatInt(cursor, 10), // exclusive
		Max: "+inf",
	}).Result()
	if err != nil {
		return nil, cursor, err
	}

	out := make(map[string]float64)
	maxSeq := cursor
	for _, z := range zs {
		m, _ := z.Member.(string)
		p := strings.SplitN(m, ":", 3) // miner:diff:seq
		if len(p) < 2 {
			continue
		}
		diff, err := strconv.ParseFloat(p[1], 64)
		if err != nil {
			continue
		}
		out[p[0]] += diff
		if int64(z.Score) > maxSeq {
			maxSeq = int64(z.Score)
		}
	}
	return out, maxSeq, nil
}

func (s *DB) GetRoundContrib(height uint64) (map[string]float64, error) {
	m, err := s.HGetAll(context.Background(), s.coin+":shares:round"+strconv.FormatUint(height, 10)).Result()
	if err != nil {
		return nil, err
	}

	contribMap := make(map[string]float64)
	for minerName, strContrib := range m {
		contrib, err := strconv.ParseFloat(strContrib, 64)
		if err != nil {
			return nil, err
		}

		contribMap[minerName] = contrib
	}

	return contribMap, nil
}
