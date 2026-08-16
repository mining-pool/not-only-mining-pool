// Package kawpow implements a pluggable engine.Engine for KawPow coins
// (Ravencoin). Unlike ethash this IS a GBT coin: the pool builds coinbase and
// merkle root exactly like the Bitcoin flow (reusing jobs.NewJob), but the
// header, PoW and stratum dialect differ:
//
//	kawpow input  = reverse(sha256d(80B header: version|prev|merkle|time|bits|height))
//	(mix, digest) = kawpow(input, height, nonce64)         // powkit, pure Go
//	block valid   = bigendian(digest) <= target(GBT)
//	full header   = 80B || nonce64(LE) || reverse(mix)     // 120 bytes
//
// Byte-order conventions verified against Ravencoin src/primitives/block.cpp
// (GetKAWPOWHeaderHash = SerializeHash(CKAWPOWInput), i.e. sha256d) and
// src/hash.cpp (KAWPOWHash feeds to_hash256(headerHash.GetHex()) — a byte
// reversal — into progpow, and converts results back via uint256S(to_hex(x)),
// another reversal).
//
// Stratum dialect (kawpowminer/t-rex style):
//
//	mining.subscribe  -> result [null, extraNonce1Hex]; miner's nonce must
//	                     start with extraNonce1 (most-significant bytes)
//	mining.authorize  -> true, then a mining.notify push
//	mining.notify     -> [jobId, headerHash, seedHash, shareTarget(32B hex),
//	                      cleanJobs, height, bits]
//	mining.submit     -> [worker, jobId, 0xnonce16, 0xheaderHash, 0xmixHash]
//
// PoW verification uses powkit's light cache (~16MB per 7500-block epoch,
// generated under ~/.powcache) — no 1GB dataset needed for verify-only.
package kawpow

import (
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/sencha-dev/powkit/kawpow"
	"golang.org/x/crypto/sha3"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/engine/worksource"
	"github.com/mining-pool/not-only-mining-pool/jobs"
	"github.com/mining-pool/not-only-mining-pool/types"
	"github.com/mining-pool/not-only-mining-pool/utils"
)

var log = logging.Logger("kawpow")

const epochLength = 7500

// diff1 is the conventional KawPow difficulty-1 share target
// (miningcore / node-kawpow-stratum convention).
var diff1, _ = new(big.Int).SetString("00000000ff000000000000000000000000000000000000000000000000000000", 16)

func init() {
	engine.Register("kawpow", func() engine.Engine { return New() })
}

// job is one unit of kawpow work derived from a GBT.
type job struct {
	id         string
	inner      *jobs.Job // reused bitcoin machinery: coinbase halves, merkle, target, txdata
	header80   []byte    // serialized CKAWPOWInput
	headerHash []byte    // reverse(sha256d(header80)) — the bytes fed to kawpow and sent to miners
	seedHash   []byte
	height     uint64
	bits       string
	coinbase   []byte // fully serialized coinbase (extranonce space fixed per job)

	mu   sync.Mutex
	seen map[string]struct{}
}

// Engine is the KawPow mining engine.
type Engine struct {
	opts         *config.Options
	dm           *daemons.DaemonManager
	pow          *kawpow.Client
	pollInterval time.Duration

	enCounter  uint32 // per-connection extranonce1 allocator
	jobCounter uint64

	mu      sync.RWMutex
	cur     *job
	history map[string]*job // last few jobs accepted for submits
}

func New() *Engine {
	return &Engine{
		pollInterval: 500 * time.Millisecond,
		history:      map[string]*job{},
	}
}

func (e *Engine) Name() string { return "kawpow" }

// NotifyMethod tells the stratum router to push work as mining.notify.
func (e *Engine) NotifyMethod() string { return "mining.notify" }

func (e *Engine) Init(opts *config.Options) error {
	e.opts = opts
	e.pow = kawpow.NewRavencoin()
	e.dm = daemons.NewDaemonManager(opts.Daemons, opts.Coin)

	gbt, err := e.dm.GetBlockTemplate()
	if err != nil {
		return err
	}
	e.acceptTemplate(gbt)

	// warm the epoch cache off the hot path so the first share doesn't stall
	go func() {
		cur := e.currentJob()
		if cur == nil {
			return
		}
		if _, _, err := e.pow.Compute(cur.headerHash, cur.height, 0); err != nil {
			log.Error("kawpow cache warmup failed: ", err)
		} else {
			log.Info("kawpow epoch cache ready for height ", cur.height)
		}
	}()
	return nil
}

// acceptTemplate builds a new job from a GBT and makes it current.
func (e *Engine) acceptTemplate(gbt *daemons.GetBlockTemplate) {
	id := strconv.FormatUint(atomic.AddUint64(&e.jobCounter, 1), 16)

	inner := jobs.NewJob(
		id,
		gbt,
		e.opts.PoolAddress.GetScript(),
		utils.HexDecode([]byte("f000000ff111111f")), // 8-byte extranonce space, fixed below
		"POW",
		e.opts.Coin.TxMessages,
		e.opts.RewardRecipients,
		nil, // RVN uses the default double-SHA256 merkle
	)

	// KawPow miners roll the 64-bit header nonce, never the coinbase — so the
	// coinbase extranonce space is pinned per job (job counter keeps it unique).
	fixed := make([]byte, 8)
	binary.BigEndian.PutUint64(fixed, e.jobCounter)
	coinbase := inner.SerializeCoinbase(fixed[:4], fixed[4:])

	merkleRoot := inner.MerkleTree.WithFirst(utils.Sha256d(coinbase)) // internal byte order

	prevHash := utils.ReverseBytes(utils.HexDecode([]byte(gbt.PreviousBlockHash))) // display -> internal

	header80 := serializeKawpowInput(uint32(gbt.Version), prevHash, merkleRoot, uint32(gbt.CurTime), gbt.Bits, uint32(gbt.Height))
	headerHash := utils.ReverseBytes(utils.Sha256d(header80))

	j := &job{
		id:         id,
		inner:      inner,
		header80:   header80,
		headerHash: headerHash,
		seedHash:   seedHash(uint64(gbt.Height)),
		height:     uint64(gbt.Height),
		bits:       gbt.Bits,
		coinbase:   coinbase,
		seen:       map[string]struct{}{},
	}

	e.mu.Lock()
	e.cur = j
	e.history[j.id] = j
	if len(e.history) > 8 { // keep a short tail for late submits
		for k := range e.history {
			if k != j.id && len(e.history) > 8 {
				delete(e.history, k)
			}
		}
	}
	e.mu.Unlock()
	log.Info("new kawpow job ", id, " height ", gbt.Height)
}

func (e *Engine) currentJob() *job {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cur
}

// Watch subscribes to the daemon's ZMQ hashblock topic when configured, with a
// poll safety net; otherwise it polls getblocktemplate.
func (e *Engine) Watch(onNewWork func()) error {
	sources := []worksource.Source{worksource.Poll(e.pollInterval, e.refresh)}
	if zmq := e.opts.Daemons[0].ZMQ; zmq != "" {
		sources = append([]worksource.Source{
			worksource.ZMQSource("bitcoind-zmq", zmq, "hashblock", e.refresh),
		}, sources...)
	}
	return worksource.Run(onNewWork, sources...)
}

func (e *Engine) refresh() (bool, error) {
	gbt, err := e.dm.GetBlockTemplate()
	if err != nil {
		return false, err
	}
	if cur := e.currentJob(); cur != nil && cur.inner.GetBlockTemplate.PreviousBlockHash == gbt.PreviousBlockHash {
		return false, nil
	}
	e.acceptTemplate(gbt)
	return true, nil
}

// OnSubscribe assigns a 2-byte extranonce1: the miner must use it as the
// most-significant bytes of every 64-bit nonce it submits.
func (e *Engine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	en := make([]byte, 2)
	binary.BigEndian.PutUint16(en, uint16(atomic.AddUint32(&e.enCounter, 1)))
	return []interface{}{nil, hex.EncodeToString(en)}, en, 6
}

// JobParamsForDifficulty builds the mining.notify params with the share target
// for the given difficulty embedded (kawpow has no mining.set_difficulty).
func (e *Engine) JobParamsForDifficulty(diff float64) []interface{} {
	cur := e.currentJob()
	if cur == nil {
		return nil
	}
	return []interface{}{
		cur.id,
		hex.EncodeToString(cur.headerHash),
		hex.EncodeToString(cur.seedHash),
		engine.TargetHex(engine.TargetFromDiff(diff1, diff)),
		true,
		cur.height,
		cur.bits,
	}
}

func (e *Engine) JobNotification(_ bool) (string, []interface{}) {
	return "mining.notify", e.JobParamsForDifficulty(1)
}

// OnSubmit validates [worker, jobId, 0xnonce, 0xheaderHash, 0xmixHash].
func (e *Engine) OnSubmit(s engine.Session, params []interface{}) *types.Share {
	share := &types.Share{Miner: s.WorkerName(), RemoteAddr: s.RemoteAddr()}

	if len(params) < 5 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	jobID, ok1 := params[1].(string)
	nonceHex, ok2 := params[2].(string)
	headerHex, ok3 := params[3].(string)
	mixHex, ok4 := params[4].(string)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	nonceHex = strings.ToLower(strings.TrimPrefix(nonceHex, "0x"))
	headerHex = strings.ToLower(strings.TrimPrefix(headerHex, "0x"))
	mixHex = strings.ToLower(strings.TrimPrefix(mixHex, "0x"))

	e.mu.RLock()
	j := e.history[jobID]
	e.mu.RUnlock()
	if j == nil || headerHex != hex.EncodeToString(j.headerHash) {
		share.ErrorCode = types.ErrJobNotFound
		return share
	}
	share.JobId = j.id
	share.BlockHeight = int64(j.height)
	share.BlockReward = uint64(j.inner.GetBlockTemplate.CoinbaseValue)

	if len(nonceHex) != 16 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	// the assigned extranonce1 must prefix the nonce (most-significant bytes)
	if en1 := s.ExtraNonce1(); len(en1) > 0 && !strings.HasPrefix(nonceHex, hex.EncodeToString(en1)) {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	nonce, err := strconv.ParseUint(nonceHex, 16, 64)
	if err != nil {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}

	j.mu.Lock()
	if _, dup := j.seen[nonceHex]; dup {
		j.mu.Unlock()
		share.ErrorCode = types.ErrDuplicateShare
		return share
	}
	j.seen[nonceHex] = struct{}{}
	j.mu.Unlock()

	mix, digest, err := e.pow.Compute(j.headerHash, j.height, nonce)
	if err != nil {
		log.Error("kawpow compute failed: ", err)
		share.ErrorCode = types.ErrJobNotFound
		return share
	}
	if mixHex != hex.EncodeToString(mix) {
		share.ErrorCode = types.ErrIncorrectNonceSize // invalid solution
		return share
	}

	digestBig := new(big.Int).SetBytes(digest)
	share.Diff = engine.DiffFromValue(diff1, digestBig)

	if digestBig.Cmp(engine.TargetFromDiff(diff1, s.Difficulty())) > 0 {
		share.ErrorCode = types.ErrLowDiffShare
		return share
	}

	// network block?
	if digestBig.Cmp(j.inner.Target) <= 0 {
		blockHex := hex.EncodeToString(j.serializeBlock(nonce, mix))
		e.dm.SubmitBlock(blockHex)
		share.BlockHex = blockHex
		// A KAWPOW block's id is its progpow final hash (Ravencoin CBlockHeader::
		// GetHash → KAWPOWHash_OnlyMix), NOT sha256d of the header — the digest the
		// PoW already produced. Using sha256d here yields a hash the node doesn't
		// know, so getblock/CheckBlockAccepted can't resolve the coinbase txid.
		share.BlockHash = hex.EncodeToString(digest)
		// Ravencoin is bitcoin-family, so resolve the coinbase txid the payout
		// processor needs to attribute this block's reward (empty if the node
		// rejected it — the share stays a normal contribution then).
		if _, tx := e.dm.CheckBlockAccepted(share.BlockHash); tx != "" {
			share.TxHash = tx
		}
		log.Warn("kawpow block candidate at height ", j.height, " hash ", share.BlockHash, " coinbase ", share.TxHash)
	}

	return share
}

// --- serialization ---

// serializeKawpowInput lays out the 80-byte CKAWPOWInput exactly like the
// Ravencoin serializer: all integers little-endian, hashes in internal order.
func serializeKawpowInput(version uint32, prevHashInternal, merkleRootInternal []byte, nTime uint32, bitsHex string, height uint32) []byte {
	b := make([]byte, 0, 80)
	b = append(b, utils.PackUint32LE(version)...)
	b = append(b, prevHashInternal...)
	b = append(b, merkleRootInternal...)
	b = append(b, utils.PackUint32LE(nTime)...)
	b = append(b, utils.ReverseBytes(utils.HexDecode([]byte(bitsHex)))...) // display hex -> LE uint32
	b = append(b, utils.PackUint32LE(height)...)
	return b
}

// fullHeader appends nNonce64 (LE) and mix_hash (internal order = reversed
// kawpow mix) to the 80-byte input: the 120-byte post-activation header.
func (j *job) fullHeader(nonce uint64, mix []byte) []byte {
	h := make([]byte, 0, 120)
	h = append(h, j.header80...)
	h = append(h, utils.PackUint64LE(nonce)...)
	h = append(h, utils.ReverseBytes(mix)...)
	return h
}

func (j *job) serializeBlock(nonce uint64, mix []byte) []byte {
	b := j.fullHeader(nonce, mix)
	b = append(b, utils.VarIntBytes(uint64(len(j.inner.GetBlockTemplate.Transactions)+1))...)
	b = append(b, j.coinbase...)
	b = append(b, j.inner.TransactionData...)
	return b
}

// --- math helpers ---

// seedHash is the ethash-style seed for the epoch of the given height:
// keccak256 iterated epoch times over 32 zero bytes (epoch = height / 7500).
func seedHash(height uint64) []byte {
	seed := make([]byte, 32)
	for i := uint64(0); i < height/epochLength; i++ {
		h := sha3.NewLegacyKeccak256()
		h.Write(seed)
		seed = h.Sum(nil)
	}
	return seed
}
