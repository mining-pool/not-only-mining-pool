// Package kaspa implements a pluggable engine.Engine for Kaspa (kHeavyHash,
// blockDAG):
//
//  1. Work source. Kaspa produces 1-10 blocks per second. Watch is driven by
//     kaspad's gRPC new-template notifications (RegisterForNewBlockTemplate...)
//     via worksource.Subscribe, with a slow worksource.Poll as a safety net.
//
//  2. Consensus code. The node speaks gRPC/protobuf and the header hash is
//     blake2b over a typed structure, so this engine depends on
//     github.com/kaspanet/kaspad (rpcclient + domain/consensus pow) rather than
//     reimplementing them. That dependency is heavy, so the engine links only
//     with `-tags kaspa`.
//
// Miner dialect (kaspa-stratum-bridge convention, spoken by lolMiner/BzMiner/
// SRBMiner):
//
//	mining.subscribe -> [true, "EthereumStratum/1.0.0"]
//	mining.set_difficulty [diff] pushed before every job
//	mining.notify [jobId, largeJob] where largeJob is 80 hex chars:
//	    4×BigEndian-uint64(prePowHash) || LittleEndian-uint64(timestamp)
//	mining.submit [worker, jobId, nonceHex]
//
// PoW: powValue = kHeavyHash(prePowHash, timestamp, nonce) as computed by
// kaspad's own pow.State — validated in tests against powkit's independent
// implementation (two codebases agreeing on synthetic headers).
//
// Share difficulty convention: target = 2^256/diff (same as the ethash engine
// here). The reference bridge only enforces the NETWORK target; miners treat
// set_difficulty as a local filter, so a laxer pool-side share target is safe.
//
// DAG accounting caveat (documented, not solved here): Kaspa confirmations are
// by blue score, and "red" (un-merged) blocks are normal. Share bookkeeping
// works as usual; PAYMENT logic must be DAG-aware and is out of scope.
package kaspa

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/kaspanet/kaspad/app/appmessage"
	"github.com/kaspanet/kaspad/domain/consensus/utils/consensushashing"
	"github.com/kaspanet/kaspad/domain/consensus/model/externalapi"
	"github.com/kaspanet/kaspad/domain/consensus/utils/pow"
	"github.com/kaspanet/kaspad/infrastructure/network/rpcclient"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/engine/worksource"
	"github.com/mining-pool/not-only-mining-pool/types"
)

var log = logging.Logger("kaspa")

func init() {
	engine.Register("kaspa", func() engine.Engine { return New() })
	engine.Register("kheavyhash", func() engine.Engine { return New() })
}

type job struct {
	id         string
	block      *externalapi.DomainBlock
	prePowHash []byte // 32 bytes, the zeroed-header hash miners work on
	timestamp  int64

	mu    sync.Mutex
	state *pow.State // holds the per-job heavyhash matrix; guarded by mu
	seen  map[string]struct{}
}

// Engine is the Kaspa mining engine.
type Engine struct {
	opts       *config.Options
	client     *rpcclient.RPCClient
	jobCounter uint64

	// templateKick is fed by the gRPC notification stream; the Watch event
	// source drains it and refreshes the template.
	templateKick chan struct{}

	mu      sync.RWMutex
	cur     *job
	history map[string]*job
}

func New() *Engine {
	return &Engine{
		templateKick: make(chan struct{}, 1),
		history:      map[string]*job{},
	}
}

func (e *Engine) Name() string { return "kaspa" }

func (e *Engine) NotifyMethod() string { return "mining.notify" }

// TargetParams: the bridge dialect pushes mining.set_difficulty before jobs.
func (e *Engine) TargetParams(diff float64) (string, []interface{}) {
	return "mining.set_difficulty", []interface{}{diff}
}

func (e *Engine) Init(opts *config.Options) error {
	e.opts = opts
	if len(opts.Daemons) == 0 {
		return errors.New("kaspa: no daemon configured")
	}

	client, err := rpcclient.NewRPCClient(fmt.Sprintf("%s:%d", opts.Daemons[0].Host, opts.Daemons[0].Port))
	if err != nil {
		return err
	}
	e.client = client

	// streaming work source: kaspad pushes template-changed events
	err = client.RegisterForNewBlockTemplateNotifications(func(_ *appmessage.NewBlockTemplateNotificationMessage) {
		select {
		case e.templateKick <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return err
	}

	_, err = e.refresh()
	return err
}

func (e *Engine) refresh() (bool, error) {
	resp, err := e.client.GetBlockTemplate(e.opts.PoolAddress.Address, "not-only-mining-pool")
	if err != nil {
		return false, err
	}
	if !resp.IsSynced && os.Getenv("KASPA_ALLOW_UNSYNCED") == "" {
		return false, errors.New("kaspa: node is not synced (set KASPA_ALLOW_UNSYNCED=1 for isolated simnet/devnet)")
	}
	block, err := appmessage.RPCBlockToDomainBlock(resp.Block)
	if err != nil {
		return false, err
	}
	e.acceptBlock(block)
	return true, nil
}

// acceptBlock builds a job from a template block. prePowHash is computed
// exactly like pow.NewState does it: header hash with nonce and timestamp
// zeroed (official consensus code path).
func (e *Engine) acceptBlock(block *externalapi.DomainBlock) {
	id := strconv.FormatUint(atomic.AddUint64(&e.jobCounter, 1), 16)

	mutable := block.Header.ToMutable()
	ts, nonce := mutable.TimeInMilliseconds(), mutable.Nonce()
	mutable.SetTimeInMilliseconds(0)
	mutable.SetNonce(0)
	prePow := consensushashing.HeaderHash(mutable)
	mutable.SetTimeInMilliseconds(ts)
	mutable.SetNonce(nonce)

	j := &job{
		id:         id,
		block:      block,
		prePowHash: prePow.ByteSlice(),
		timestamp:  ts,
		state:      pow.NewState(block.Header.ToMutable()),
		seen:       map[string]struct{}{},
	}

	e.mu.Lock()
	e.cur = j
	e.history[j.id] = j
	if len(e.history) > 16 { // 1-10 bps: keep a deeper tail than slow chains
		for k := range e.history {
			if k != j.id && len(e.history) > 16 {
				delete(e.history, k)
			}
		}
	}
	e.mu.Unlock()
}

func (e *Engine) currentJob() *job {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cur
}

// Watch is driven by kaspad's gRPC new-template notifications (fed into
// templateKick from Init); a slow poll backs it up against dropped events.
func (e *Engine) Watch(onNewWork func()) error {
	events := worksource.Subscribe("kaspad-grpc", func(onEvent func()) error {
		for range e.templateKick {
			onEvent()
		}
		return errors.New("kaspa template stream closed")
	}, e.refresh)
	return worksource.Run(onNewWork, events, worksource.Poll(5*time.Second, e.refresh))
}

// OnSubscribe follows the bridge convention.
func (e *Engine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	return []interface{}{true, "EthereumStratum/1.0.0"}, nil, 0
}

// JobParamsForDifficulty renders the "large job" notify params.
func (e *Engine) JobParamsForDifficulty(_ float64) []interface{} {
	j := e.currentJob()
	if j == nil {
		return nil
	}
	return []interface{}{j.id, largeJobParams(j.prePowHash, j.timestamp)}
}

func (e *Engine) JobNotification(_ bool) (string, []interface{}) {
	return "mining.notify", e.JobParamsForDifficulty(1)
}

// OnSubmit validates [worker, jobId, nonceHex] with kaspad's own pow.State.
func (e *Engine) OnSubmit(s engine.Session, params []interface{}) *types.Share {
	share := &types.Share{Miner: s.WorkerName(), RemoteAddr: s.RemoteAddr()}

	if len(params) < 3 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	jobID, ok1 := params[1].(string)
	nonceHex, ok2 := params[2].(string)
	if !ok1 || !ok2 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	nonceHex = strings.ToLower(strings.TrimPrefix(nonceHex, "0x"))

	e.mu.RLock()
	j := e.history[jobID]
	e.mu.RUnlock()
	if j == nil {
		share.ErrorCode = types.ErrJobNotFound
		return share
	}
	share.JobId = j.id
	if j.block != nil {
		share.BlockHeight = int64(j.block.Header.DAAScore()) // DAG: DAA score, not a chain height
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

	// official consensus PoW; the state owns the per-job heavyhash matrix
	j.state.Nonce = nonce
	powValue := j.state.CalculateProofOfWorkValue()
	isBlock := powValue.Cmp(&j.state.Target) <= 0
	j.mu.Unlock()

	share.Diff = engine.DiffFromValue(engine.Pow256, powValue)

	if share.Diff < s.Difficulty() {
		share.ErrorCode = types.ErrLowDiffShare
		return share
	}

	if isBlock {
		mutable := j.block.Header.ToMutable()
		mutable.SetNonce(nonce)
		solved := &externalapi.DomainBlock{
			Header:       mutable.ToImmutable(),
			Transactions: j.block.Transactions,
		}
		rejectReason, err := e.client.SubmitBlock(solved)
		if err != nil || rejectReason != appmessage.RejectReasonNone {
			log.Error("kaspa submit block failed: reason=", rejectReason, " err=", err)
		} else {
			share.BlockHash = consensushashing.BlockHash(solved).String()
			log.Warn("kaspa block accepted, DAA score ", j.block.Header.DAAScore(), " hash ", share.BlockHash)
		}
	}

	return share
}

// --- helpers ---

// largeJobParams encodes the bridge "large job": 4 big-endian uint64 words of
// the prePowHash followed by the timestamp as a little-endian uint64, as an
// 80-char hex string.
func largeJobParams(prePowHash []byte, timestamp int64) string {
	tsBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(tsBytes, uint64(timestamp))
	return fmt.Sprintf("%016x%016x%016x%016x%016x",
		binary.BigEndian.Uint64(prePowHash[0:]),
		binary.BigEndian.Uint64(prePowHash[8:]),
		binary.BigEndian.Uint64(prePowHash[16:]),
		binary.BigEndian.Uint64(prePowHash[24:]),
		binary.LittleEndian.Uint64(tsBytes),
	)
}


