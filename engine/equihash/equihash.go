// Package equihash implements a pluggable engine.Engine for Equihash coins:
// Zcash (200,9 "ZcashPoW") and Flux/ZelHash (125,4 "ZelProof").
//
// These are GBT coins with a zcash-style header — and the node hands the pool
// a COMPLETE coinbase (`coinbasetxn`, funding streams included), so unlike the
// Bitcoin flow there is no coinbase assembly at all. The template is fixed;
// miners roll their slice of the 32-byte header nonce plus the solution:
//
//	header(140B) = version(LE4) | prevhash(32) | merkleroot(32) |
//	               hashBlockCommitments(32) | time(LE4) | bits(LE4) |
//	               nonce(32 = nonce1[pool] || nonce2[miner])
//	block hash   = sha256d(header || varint(solLen) || solution)
//	share valid  = equihash.Verify(header, solution) && hashNum <= shareTarget
//	block valid  = hashNum <= target(GBT)
//
// Stratum dialect (str4d zcash stratum):
//
//	mining.subscribe  -> [sessionId(null), nonce1Hex]
//	mining.authorize  -> true, then mining.set_target + mining.notify pushes
//	mining.notify     -> [jobId, versionLE, prevhash, merkleroot, reserved,
//	                      ntimeLE, nbitsLE, cleanJobs]   (all internal-order hex)
//	mining.submit     -> [worker, jobId, ntimeLE, nonce2Hex, solutionHex]
//
// PoW verification is powkit's pure-Go equihash (blake2b based, no cache/DAG).
package equihash

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/sencha-dev/powkit/equihash"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/engine/worksource"
	"github.com/mining-pool/not-only-mining-pool/types"
	"github.com/mining-pool/not-only-mining-pool/utils"
)

var log = logging.Logger("equihash")

// diff1 is the conventional Equihash difficulty-1 share target (the zcash
// mainnet powLimit, 0x0007ffff << 232).
var diff1, _ = new(big.Int).SetString("0007ffff00000000000000000000000000000000000000000000000000000000", 16)

const nonce1Size = 4 // pool-assigned prefix; miners roll the remaining 28 bytes

func init() {
	engine.Register("equihash", func() engine.Engine { return New() })
	engine.Register("zelhash", func() engine.Engine { return New() })
}

// variant describes one Equihash parameterization.
type variant struct {
	n, k     uint32
	client   *equihash.Client
	solBytes int // minimal-encoding solution size
}

func variantFor(name string) (*variant, error) {
	var v variant
	switch strings.ToLower(name) {
	case "", "equihash", "zcash", "equihash200_9":
		v = variant{n: 200, k: 9, client: equihash.NewZCash()}
	case "zelhash", "flux", "equihash125_4":
		v = variant{n: 125, k: 4, client: equihash.NewFlux()}
	default:
		return nil, errors.New("unknown equihash variant: " + name)
	}
	v.solBytes = int((1 << v.k) * (v.n/(v.k+1) + 1) / 8)
	return &v, nil
}

// zGBT is the zcashd getblocktemplate subset this engine needs. It is parsed
// locally (the shared daemons.GetBlockTemplate lacks the zcash fields).
type zGBT struct {
	Version              int32  `json:"version"`
	PreviousBlockHash    string `json:"previousblockhash"`
	FinalSaplingRootHash string `json:"finalsaplingroothash"`
	DefaultRoots         struct {
		MerkleRoot           string `json:"merkleroot"`
		BlockCommitmentsHash string `json:"blockcommitmentshash"`
	} `json:"defaultroots"`
	Bits        string `json:"bits"`
	Target      string `json:"target"`
	CurTime     uint32 `json:"curtime"`
	Height      int64  `json:"height"`
	CoinbaseTxn struct {
		Data string `json:"data"`
	} `json:"coinbasetxn"`
	Transactions []struct {
		Data string `json:"data"`
	} `json:"transactions"`
}

type job struct {
	id        string
	prefix108 []byte // header bytes before the nonce
	ntimeLE   string // hex, echoed by miners
	target    *big.Int
	txCount   int
	txData    []byte // coinbase + txs, serialized
	prevHash  string // GBT display hex, for staleness checks
	height    int64

	// notify params (internal-order hex)
	verHex, prevHex, merkleHex, reservedHex, bitsHex string

	mu   sync.Mutex
	seen map[string]struct{}
}

// Engine is the Equihash mining engine.
type Engine struct {
	opts         *config.Options
	dm           *daemons.DaemonManager
	v            *variant
	pollInterval time.Duration

	enCounter  uint32
	jobCounter uint64

	mu      sync.RWMutex
	cur     *job
	history map[string]*job
}

func New() *Engine {
	return &Engine{pollInterval: 500 * time.Millisecond, history: map[string]*job{}}
}

func (e *Engine) Name() string { return "equihash" }

func (e *Engine) NotifyMethod() string { return "mining.notify" }

// TargetParams implements the zcash-dialect mining.set_target pre-push.
func (e *Engine) TargetParams(diff float64) (string, []interface{}) {
	return "mining.set_target", []interface{}{engine.TargetHex(engine.TargetFromDiff(diff1, diff))}
}

func (e *Engine) Init(opts *config.Options) error {
	e.opts = opts

	v, err := variantFor(opts.Algorithm.Name)
	if err != nil {
		return err
	}
	e.v = v

	e.dm = daemons.NewDaemonManager(opts.Daemons, opts.Coin)
	gbt, err := e.getTemplate()
	if err != nil {
		return err
	}
	return e.acceptTemplate(gbt)
}

// getTemplate fetches and parses a zcash-style template (zcashd rejects
// unknown rules, so this deliberately does not reuse the segwit GBT call).
func (e *Engine) getTemplate() (*zGBT, error) {
	_, result, _ := e.dm.Cmd("getblocktemplate", []interface{}{
		map[string]interface{}{"capabilities": []string{"coinbasetxn", "workid"}},
	})
	if result == nil {
		return nil, errors.New("getblocktemplate returned nothing")
	}
	if result.Error != nil {
		return nil, errors.New("getblocktemplate failed: " + result.Error.Message)
	}
	var gbt zGBT
	if err := json.Unmarshal(result.Result, &gbt); err != nil {
		return nil, err
	}
	if gbt.CoinbaseTxn.Data == "" {
		return nil, errors.New("node did not provide coinbasetxn — equihash engine requires it (zcashd/fluxd do)")
	}
	return &gbt, nil
}

func (e *Engine) acceptTemplate(gbt *zGBT) error {
	id := strconv.FormatUint(atomic.AddUint64(&e.jobCounter, 1), 16)

	coinbase := utils.HexDecode([]byte(gbt.CoinbaseTxn.Data))
	txData := make([]byte, 0, len(coinbase))
	txData = append(txData, coinbase...)
	rawTxs := make([][]byte, 0, len(gbt.Transactions))
	for i := range gbt.Transactions {
		raw := utils.HexDecode([]byte(gbt.Transactions[i].Data))
		rawTxs = append(rawTxs, raw)
		txData = append(txData, raw...)
	}

	// merkle root: prefer the node's defaultroots, else compute bitcoin-style
	var merkle []byte
	if gbt.DefaultRoots.MerkleRoot != "" {
		merkle = utils.ReverseBytes(utils.HexDecode([]byte(gbt.DefaultRoots.MerkleRoot)))
	} else {
		merkle = merkleRoot(append([][]byte{coinbase}, rawTxs...))
	}

	// hashBlockCommitments: NU5 name, then sapling name, else zeroes (regtest genesis era)
	reserved := make([]byte, 32)
	if gbt.DefaultRoots.BlockCommitmentsHash != "" {
		reserved = utils.ReverseBytes(utils.HexDecode([]byte(gbt.DefaultRoots.BlockCommitmentsHash)))
	} else if gbt.FinalSaplingRootHash != "" {
		reserved = utils.ReverseBytes(utils.HexDecode([]byte(gbt.FinalSaplingRootHash)))
	}

	prev := utils.ReverseBytes(utils.HexDecode([]byte(gbt.PreviousBlockHash)))

	prefix := make([]byte, 0, 108)
	prefix = append(prefix, utils.PackUint32LE(uint32(gbt.Version))...)
	prefix = append(prefix, prev...)
	prefix = append(prefix, merkle...)
	prefix = append(prefix, reserved...)
	prefix = append(prefix, utils.PackUint32LE(gbt.CurTime)...)
	prefix = append(prefix, utils.ReverseBytes(utils.HexDecode([]byte(gbt.Bits)))...)

	target, ok := new(big.Int).SetString(gbt.Target, 16)
	if !ok || target.Sign() <= 0 {
		target = utils.BigIntFromBitsHex(gbt.Bits)
	}

	j := &job{
		id:          id,
		prefix108:   prefix,
		ntimeLE:     hex.EncodeToString(utils.PackUint32LE(gbt.CurTime)),
		target:      target,
		txCount:     len(gbt.Transactions) + 1,
		txData:      txData,
		prevHash:    gbt.PreviousBlockHash,
		height:      gbt.Height,
		verHex:      hex.EncodeToString(utils.PackUint32LE(uint32(gbt.Version))),
		prevHex:     hex.EncodeToString(prev),
		merkleHex:   hex.EncodeToString(merkle),
		reservedHex: hex.EncodeToString(reserved),
		bitsHex:     hex.EncodeToString(utils.ReverseBytes(utils.HexDecode([]byte(gbt.Bits)))),
		seen:        map[string]struct{}{},
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
	log.Info("new equihash job ", id, " height ", gbt.Height)
	return nil
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
			worksource.ZMQSource("zcashd-zmq", zmq, "hashblock", e.refresh),
		}, sources...)
	}
	return worksource.Run(onNewWork, sources...)
}

func (e *Engine) refresh() (bool, error) {
	gbt, err := e.getTemplate()
	if err != nil {
		return false, err
	}
	if cur := e.currentJob(); cur != nil && cur.prevHash == gbt.PreviousBlockHash {
		return false, nil
	}
	if err := e.acceptTemplate(gbt); err != nil {
		return false, err
	}
	return true, nil
}

// OnSubscribe assigns nonce1: the first nonce1Size bytes of the 32-byte nonce.
func (e *Engine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	en := make([]byte, nonce1Size)
	binary.BigEndian.PutUint32(en, atomic.AddUint32(&e.enCounter, 1))
	return []interface{}{nil, hex.EncodeToString(en)}, en, 32 - nonce1Size
}

// JobParamsForDifficulty: equihash notify params carry no target (it travels in
// mining.set_target), so difficulty does not change the payload.
func (e *Engine) JobParamsForDifficulty(_ float64) []interface{} {
	j := e.currentJob()
	if j == nil {
		return nil
	}
	return []interface{}{j.id, j.verHex, j.prevHex, j.merkleHex, j.reservedHex, j.ntimeLE, j.bitsHex, true}
}

func (e *Engine) JobNotification(_ bool) (string, []interface{}) {
	return "mining.notify", e.JobParamsForDifficulty(1)
}

// OnSubmit validates [worker, jobId, ntimeLE, nonce2Hex, solutionHex].
func (e *Engine) OnSubmit(s engine.Session, params []interface{}) *types.Share {
	share := &types.Share{Miner: s.WorkerName(), RemoteAddr: s.RemoteAddr()}

	if len(params) < 5 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	jobID, ok1 := params[1].(string)
	ntime, ok2 := params[2].(string)
	nonce2Hex, ok3 := params[3].(string)
	solHex, ok4 := params[4].(string)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	nonce2Hex = strings.ToLower(strings.TrimPrefix(nonce2Hex, "0x"))
	solHex = strings.ToLower(strings.TrimPrefix(solHex, "0x"))

	e.mu.RLock()
	j := e.history[jobID]
	e.mu.RUnlock()
	if j == nil {
		share.ErrorCode = types.ErrJobNotFound
		return share
	}
	share.JobId = j.id
	share.BlockHeight = j.height

	if !strings.EqualFold(ntime, j.ntimeLE) {
		share.ErrorCode = types.ErrNTimeOutOfRange
		return share
	}

	en1 := s.ExtraNonce1()
	if len(en1) == 0 || len(nonce2Hex) != (32-len(en1))*2 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	nonce2, err := hex.DecodeString(nonce2Hex)
	if err != nil {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}

	// solution: accept with or without the compact-size prefix
	sol, err := hex.DecodeString(solHex)
	if err != nil {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	if len(sol) != e.v.solBytes {
		trimmed := trimSolutionPrefix(sol, e.v.solBytes)
		if trimmed == nil {
			share.ErrorCode = types.ErrIncorrectNonceSize
			return share
		}
		sol = trimmed
	}

	j.mu.Lock()
	key := nonce2Hex
	if _, dup := j.seen[key]; dup {
		j.mu.Unlock()
		share.ErrorCode = types.ErrDuplicateShare
		return share
	}
	j.seen[key] = struct{}{}
	j.mu.Unlock()

	header := make([]byte, 0, 140)
	header = append(header, j.prefix108...)
	header = append(header, en1...)
	header = append(header, nonce2...)

	valid, err := e.v.client.Verify(header, sol)
	if err != nil || !valid {
		share.ErrorCode = types.ErrIncorrectNonceSize // invalid solution
		return share
	}

	full := make([]byte, 0, 140+3+len(sol))
	full = append(full, header...)
	full = append(full, utils.VarIntBytes(uint64(len(sol)))...)
	full = append(full, sol...)

	hashNum := new(big.Int).SetBytes(utils.ReverseBytes(utils.Sha256d(full)))
	share.Diff = engine.DiffFromValue(diff1, hashNum)

	if hashNum.Cmp(engine.TargetFromDiff(diff1, s.Difficulty())) > 0 {
		share.ErrorCode = types.ErrLowDiffShare
		return share
	}

	if hashNum.Cmp(j.target) <= 0 {
		block := make([]byte, 0, len(full)+9+len(j.txData))
		block = append(block, full...)
		block = append(block, utils.VarIntBytes(uint64(j.txCount))...)
		block = append(block, j.txData...)
		blockHex := hex.EncodeToString(block)
		e.dm.SubmitBlock(blockHex)
		share.BlockHex = blockHex
		share.BlockHash = hex.EncodeToString(utils.ReverseBytes(utils.Sha256d(full)))
		log.Warn("equihash block candidate at height ", j.height, " hash ", share.BlockHash)
	}

	return share
}

// --- helpers ---

// trimSolutionPrefix strips a leading bitcoin compact-size length prefix when
// the remaining bytes match the expected minimal solution size.
func trimSolutionPrefix(sol []byte, want int) []byte {
	for _, p := range []int{1, 3, 5} { // 0xXX | 0xfd.. | 0xfe....
		if len(sol) == want+p {
			return sol[p:]
		}
	}
	return nil
}

// merkleRoot computes the bitcoin-style merkle root (internal order) over raw
// transactions, duplicating the last hash on odd levels.
func merkleRoot(rawTxs [][]byte) []byte {
	level := make([][]byte, len(rawTxs))
	for i := range rawTxs {
		level[i] = utils.Sha256d(rawTxs[i])
	}
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([][]byte, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			next[i/2] = utils.Sha256d(append(append([]byte{}, level[i]...), level[i+1]...))
		}
		level = next
	}
	return level[0]
}



