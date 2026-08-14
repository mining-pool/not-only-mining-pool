// Package cryptonote implements a pluggable engine.Engine for CryptoNote
// coins (Monero). The node builds the whole block: get_block_template returns
// a complete block blob with a miner tx paying the pool address and a reserved
// slot in tx_extra for the pool's extranonce. The pool pokes the extranonce in,
// recomputes miner-tx-hash → tree_hash → hashing blob (see blob.go, pinned to
// Monero's reference C code), and hands miners the hashing blob; miners roll
// the 4-byte header nonce and return nonce + the RandomX result hash.
//
// Stratum dialect (XMRig): login/getjob/submit/keepalived with OBJECT params —
// the stratum router's ObjectParams capability handles the shape.
//
// PoW verification is RandomX via the updated mining-pool/go-randomx binding
// (cgo, light mode ~256MB). It is compiled in ONLY with `-tags randomx`; the
// default build registers the engine but Init fails with a clear message.
package cryptonote

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/engine/worksource"
	"github.com/mining-pool/not-only-mining-pool/types"
	"github.com/mining-pool/not-only-mining-pool/utils"
)

var log = logging.Logger("cryptonote")

// pow64 is 2^64, used to pack the XMRig compact share target.
var pow64 = new(big.Int).Lsh(big.NewInt(1), 64)

// powHasher verifies RandomX PoW. Wired by rx_cgo.go (-tags randomx) or left
// nil by rx_stub.go, in which case Init refuses to start.
type powHasher interface {
	Hash(seed, input []byte) ([]byte, error)
	Close()
}

// newPowHasher is set by the build-tagged file that links RandomX.
var newPowHasher func() (powHasher, error)

func init() {
	engine.Register("cryptonote", func() engine.Engine { return New() })
	engine.Register("randomx", func() engine.Engine { return New() })
}

type job struct {
	id       string
	blob     []byte // blocktemplate_blob with the pool extranonce applied
	hashing  []byte // hashing blob (nonce zeroed)
	nonceOff int
	seed     []byte
	height   int64
	diff     uint64 // network difficulty
	prevHash string

	mu   sync.Mutex
	seen map[string]struct{}
}

// Engine is the CryptoNote/RandomX mining engine.
type Engine struct {
	opts         *config.Options
	rpc          *xmrRPC
	pow          powHasher
	pollInterval time.Duration
	jobCounter   uint64

	mu      sync.RWMutex
	cur     *job
	history map[string]*job
}

func New() *Engine {
	return &Engine{pollInterval: time.Second, history: map[string]*job{}}
}

func (e *Engine) Name() string { return "cryptonote" }

// NotifyMethod: XMRig job pushes use method "job" ...
func (e *Engine) NotifyMethod() string { return "job" }

// ObjectParams: ... with a bare JSON object as params.
func (e *Engine) ObjectParams() bool { return true }

func (e *Engine) Init(opts *config.Options) error {
	if newPowHasher == nil {
		return errors.New("cryptonote engine requires RandomX: rebuild with `-tags randomx`")
	}
	pow, err := newPowHasher()
	if err != nil {
		return err
	}
	e.pow = pow

	e.opts = opts
	if len(opts.Daemons) == 0 {
		return errors.New("cryptonote: no daemon configured")
	}
	e.rpc = newXmrRPC(opts.Daemons[0].URL())

	gbt, err := e.rpc.GetBlockTemplate(opts.PoolAddress.Address, 8)
	if err != nil {
		return err
	}
	return e.acceptTemplate(gbt)
}

func (e *Engine) acceptTemplate(t *blockTemplate) error {
	id := strconv.FormatUint(atomic.AddUint64(&e.jobCounter, 1), 16)

	blob, err := hex.DecodeString(t.BlocktemplateBlob)
	if err != nil {
		return err
	}
	seed, err := hex.DecodeString(t.SeedHash)
	if err != nil || len(seed) != 32 {
		return errors.New("cryptonote: bad seed_hash in template")
	}

	// poke our job extranonce into the reserved tx_extra slot; the miner tx
	// hash changes, so the hashing blob must be rebuilt from scratch
	if t.ReservedOffset <= 0 || t.ReservedOffset+8 > len(blob) {
		return errors.New("cryptonote: reserved_offset out of range")
	}
	binary.LittleEndian.PutUint64(blob[t.ReservedOffset:], e.jobCounter)

	p, err := parseBlockBlob(blob)
	if err != nil {
		return err
	}

	j := &job{
		id:       id,
		blob:     blob,
		hashing:  p.hashingBlob(),
		nonceOff: p.nonceOffset,
		seed:     seed,
		height:   t.Height,
		diff:     t.Difficulty,
		prevHash: t.PrevHash,
		seen:     map[string]struct{}{},
	}

	e.mu.Lock()
	e.cur = j
	e.history[j.id] = j
	if len(e.history) > 8 {
		for k := range e.history {
			if k != j.id && len(e.history) > 8 {
				delete(e.history, k)
			}
		}
	}
	e.mu.Unlock()
	log.Info("new cryptonote job ", id, " height ", t.Height, " diff ", t.Difficulty)
	return nil
}

func (e *Engine) currentJob() *job {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cur
}

// Watch subscribes to monerod's ZMQ chain-tip notifications when a zmq endpoint
// is configured, with a poll safety net; otherwise it polls get_block_template.
func (e *Engine) Watch(onNewWork func()) error {
	sources := []worksource.Source{worksource.Poll(e.pollInterval, e.refresh)}
	if zmq := e.opts.Daemons[0].ZMQ; zmq != "" {
		sources = append([]worksource.Source{
			worksource.ZMQSource("monerod-zmq", zmq, "json-minimal-chain_main", e.refresh),
		}, sources...)
	}
	return worksource.Run(onNewWork, sources...)
}

func (e *Engine) refresh() (bool, error) {
	tmpl, err := e.rpc.GetBlockTemplate(e.opts.PoolAddress.Address, 8)
	if err != nil {
		return false, err
	}
	if cur := e.currentJob(); cur != nil && cur.prevHash == tmpl.PrevHash {
		return false, nil
	}
	if err := e.acceptTemplate(tmpl); err != nil {
		return false, err
	}
	return true, nil
}

// OnSubscribe handles XMRig login: the reply must embed the first job.
func (e *Engine) OnSubscribe(s engine.Session, _ []interface{}) (interface{}, []byte, int) {
	return map[string]interface{}{
		"id":     "1",
		"job":    e.jobObject(s.Difficulty()),
		"status": "OK",
	}, nil, 0
}

// jobObject renders the XMRig job payload for a share difficulty.
func (e *Engine) jobObject(diff float64) map[string]interface{} {
	j := e.currentJob()
	if j == nil {
		return nil
	}
	return map[string]interface{}{
		"job_id":    j.id,
		"blob":      hex.EncodeToString(j.hashing),
		"target":    targetHex(diff),
		"algo":      "rx/0",
		"height":    j.height,
		"seed_hash": hex.EncodeToString(j.seed),
	}
}

func (e *Engine) JobParamsForDifficulty(diff float64) []interface{} {
	obj := e.jobObject(diff)
	if obj == nil {
		return nil
	}
	return []interface{}{obj}
}

func (e *Engine) JobNotification(_ bool) (string, []interface{}) {
	return "job", e.JobParamsForDifficulty(1)
}

// OnSubmit validates {"job_id","nonce","result"}: recompute RandomX over the
// hashing blob with the miner's nonce, require it to match the claimed result,
// then check share/network difficulty.
func (e *Engine) OnSubmit(s engine.Session, params []interface{}) *types.Share {
	share := &types.Share{Miner: s.WorkerName(), RemoteAddr: s.RemoteAddr()}

	if len(params) != 1 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	obj, ok := params[0].(map[string]interface{})
	if !ok {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	jobID, _ := obj["job_id"].(string)
	nonceHex, _ := obj["nonce"].(string)
	resultHex, _ := obj["result"].(string)
	nonceHex = strings.ToLower(strings.TrimPrefix(nonceHex, "0x"))
	resultHex = strings.ToLower(resultHex)

	e.mu.RLock()
	j := e.history[jobID]
	e.mu.RUnlock()
	if j == nil {
		share.ErrorCode = types.ErrJobNotFound
		return share
	}
	share.JobId = j.id
	share.BlockHeight = j.height

	nonce, err := hex.DecodeString(nonceHex)
	if err != nil || len(nonce) != 4 || len(resultHex) != 64 {
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

	input := withNonce(j.hashing, j.nonceOff, nonce)
	hash, err := e.pow.Hash(j.seed, input)
	if err != nil {
		log.Error("randomx hash failed: ", err)
		share.ErrorCode = types.ErrJobNotFound
		return share
	}
	// anti-cheat: the miner's claimed result must match the recomputation
	if hex.EncodeToString(hash) != resultHex {
		share.ErrorCode = types.ErrIncorrectNonceSize // invalid result
		return share
	}

	// CryptoNote: hash bytes are a little-endian 256-bit number
	hashNum := new(big.Int).SetBytes(utils.ReverseBytes(hash))
	share.Diff = engine.DiffFromValue(engine.Pow256, hashNum)

	if share.Diff < s.Difficulty() {
		share.ErrorCode = types.ErrLowDiffShare
		return share
	}

	// network block?
	if j.diff > 0 && share.Diff >= float64(j.diff) {
		blockBlob := withNonce(j.blob, j.nonceOff, nonce)
		blockHex := hex.EncodeToString(blockBlob)
		if err := e.rpc.SubmitBlock(blockHex); err != nil {
			log.Error("cryptonote submit_block failed: ", err)
		} else {
			share.BlockHex = blockHex
			share.BlockHash = resultHex
			log.Warn("cryptonote block candidate at height ", j.height)
		}
	}

	return share
}

// --- helpers ---



// targetHex renders the XMRig compact share target: floor(2^64 / diff) as
// 8 little-endian bytes in hex.
func targetHex(diff float64) string {
	if diff < 1 {
		diff = 1
	}
	t, _ := new(big.Float).Quo(
		new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(2), big.NewInt(64), nil)),
		big.NewFloat(diff),
	).Int(nil)
	if t.BitLen() > 64 {
		t.SetUint64(1<<64 - 1)
	}
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, t.Uint64())
	return hex.EncodeToString(b)
}
