// Package ethash implements a pluggable engine.Engine for Ethash / Etchash
// coins (Ethereum Classic and friends).
//
// This is a "node-builds-the-block" engine: unlike the bitcoin GBT flow the
// pool does NOT assemble coinbase/merkle/header. Instead it brokers work:
//
//	eth_getWork  -> [headerHash, seedHash, target, (blockNumber)]
//	miner solves -> nonce + mixHash
//	eth_submitWork(nonce, headerHash, mixHash) -> node seals & broadcasts
//
// Shared pool infrastructure (stratum TCP server, vardiff, banning, redis
// storage, payments, API) is reused verbatim; only node interaction, PoW
// verification (via go-etchash light cache) and the ethproxy stratum dialect
// live here.
//
// Status: the deterministic, node-facing logic (RPC, target math, PoW verify)
// is implemented and unit-tested. Wiring the engine into the stratum message
// router (so eth_submitLogin/eth_getWork/eth_submitWork reach these methods) is
// the integration step; see docs/PLUGGABLE_ENGINES_zh.md. It must be validated
// against a live ETC/core-geth private chain (no daemon runs in CI).
package ethash

import (
	"errors"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	etchash "github.com/etclabscore/go-etchash"
	"github.com/ethereum/go-ethereum/common"
	logging "github.com/ipfs/go-log/v2"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/engine/worksource"
	"github.com/mining-pool/not-only-mining-pool/types"
)

var log = logging.Logger("ethash")

// ECIP1099FBlock is the ECIP-1099 epoch-length-doubling activation block.
// Default is ETC mainnet (11_700_000). Override via ETC_ECIP1099_FBLOCK, or set
// to a huge value / disable for chains that never activate it (e.g. Mordor is
// 2_520_000; a private ethash chain may never activate it).
var ECIP1099FBlock uint64 = 11_700_000

func init() {
	engine.Register("ethash", func() engine.Engine { return New() })
	engine.Register("etchash", func() engine.Engine { return New() })
}

type work struct {
	jobID       string
	headerHash  string   // 0x-prefixed
	seedHash    string   // 0x-prefixed
	target      *big.Int // network target
	blockNumber uint64
}

// Engine is the Ethash/Etchash mining engine.
type Engine struct {
	rpc          *ethRPC
	light        *etchash.Etchash
	pollInterval time.Duration

	mu      sync.RWMutex
	cur     *work
	seen    map[string]struct{} // headerHash|nonce dedup, reset per new work
}

func New() *Engine {
	return &Engine{pollInterval: 500 * time.Millisecond, seen: map[string]struct{}{}}
}

func (e *Engine) Name() string { return "ethash" }

func (e *Engine) Init(opts *config.Options) error {
	if len(opts.Daemons) == 0 {
		return errors.New("ethash: no daemon configured")
	}
	e.rpc = newEthRPC(opts.Daemons[0].URL())

	ecip := ECIP1099FBlock
	if v := os.Getenv("ETC_ECIP1099_FBLOCK"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			ecip = n
		}
	}
	e.light = etchash.New(&ecip, nil)

	_, err := e.RefreshWork()
	return err
}

// RefreshWork fetches the latest work from the node. It returns true when the
// header hash changed (i.e. miners must be re-notified).
func (e *Engine) RefreshWork() (changed bool, err error) {
	w, err := e.rpc.GetWork()
	if err != nil {
		return false, err
	}

	target, ok := hexToBig(w[2])
	if !ok {
		return false, errors.New("ethash: bad target in getWork: " + w[2])
	}

	var blockNumber uint64
	if len(w) >= 4 {
		if n, ok := hexToBig(w[3]); ok {
			blockNumber = n.Uint64()
		}
	}
	if blockNumber == 0 {
		if hn, err := e.rpc.BlockNumber(); err == nil {
			if n, ok := hexToBig(hn); ok {
				blockNumber = n.Uint64() + 1 // work is for head+1
			}
		}
	}

	nw := &work{
		jobID:       shortJobID(w[0]),
		headerHash:  w[0],
		seedHash:    w[1],
		target:      target,
		blockNumber: blockNumber,
	}

	e.mu.Lock()
	changed = e.cur == nil || e.cur.headerHash != nw.headerHash
	e.cur = nw
	if changed {
		e.seen = map[string]struct{}{}
	}
	e.mu.Unlock()
	return changed, nil
}

// CurrentWork returns a snapshot of the current work (nil before the first fetch).
func (e *Engine) CurrentWork() (jobID, headerHash, seedHash string, blockNumber uint64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cur == nil {
		return "", "", "", 0
	}
	return e.cur.jobID, e.cur.headerHash, e.cur.seedHash, e.cur.blockNumber
}

// JobParamsForDifficulty builds the ethproxy work package a miner receives,
// with the per-connection share target derived from diff (miners submit any
// solution below this target as a share; solutions below the network target are
// blocks). Returns nil before the first work is fetched.
func (e *Engine) JobParamsForDifficulty(diff float64) []interface{} {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cur == nil {
		return nil
	}
	shareTarget := TargetFromDifficulty(diff)
	return []interface{}{
		e.cur.headerHash,
		e.cur.seedHash,
		"0x" + engine.TargetHex(shareTarget),
	}
}

// Watch polls eth_getWork; geth exposes new work over HTTP request/response
// (its push channel is WebSocket eth_subscribe, not wired here).
func (e *Engine) Watch(onNewWork func()) error {
	return worksource.Run(onNewWork, worksource.Poll(e.pollInterval, e.RefreshWork))
}

// OnSubscribe handles the ethproxy eth_submitLogin. Ethash has no extranonce.
func (e *Engine) OnSubscribe(_ engine.Session, _ []interface{}) (result interface{}, extraNonce1 []byte, extraNonce2Size int) {
	return true, nil, 0
}

// JobNotification returns the ethproxy work push. Note: the target here is the
// NETWORK target; the stratum integration should instead send the per-client
// package from JobParamsForDifficulty(clientDiff). Kept for interface symmetry.
func (e *Engine) JobNotification(_ bool) (method string, params []interface{}) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cur == nil {
		return "", nil
	}
	return "", []interface{}{e.cur.headerHash, e.cur.seedHash, "0x" + engine.TargetHex(e.cur.target)}
}

// OnSubmit validates an ethproxy eth_submitWork submission:
// params = [nonce, headerHash, mixHash] (all 0x hex). It verifies the PoW with
// go-etchash, checks the share/network target, submits blocks to the node, and
// returns a Share for accounting.
func (e *Engine) OnSubmit(s engine.Session, params []interface{}) *types.Share {
	share := &types.Share{
		Miner:      s.WorkerName(),
		RemoteAddr: s.RemoteAddr(),
	}

	nonceHex, h1, mixHex, ok := parseSubmit(params)
	if !ok {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}

	e.mu.RLock()
	cur := e.cur
	e.mu.RUnlock()
	if cur == nil || !strings.EqualFold(h1, cur.headerHash) {
		share.ErrorCode = types.ErrJobNotFound
		return share
	}
	share.JobId = cur.jobID
	share.BlockHeight = int64(cur.blockNumber)

	nonce, err := strconv.ParseUint(strings.TrimPrefix(nonceHex, "0x"), 16, 64)
	if err != nil {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}

	// dedup identical (header, nonce) submissions
	key := cur.headerHash + "|" + nonceHex
	e.mu.Lock()
	if _, dup := e.seen[key]; dup {
		e.mu.Unlock()
		share.ErrorCode = types.ErrDuplicateShare
		return share
	}
	e.seen[key] = struct{}{}
	e.mu.Unlock()

	mixDigest, result := e.light.Compute(cur.blockNumber, common.HexToHash(cur.headerHash), nonce)

	// anti-cheat: the miner-supplied mixHash must match the recomputed one
	if !strings.EqualFold(strings.TrimPrefix(mixHex, "0x"), strings.TrimPrefix(mixDigest.Hex(), "0x")) {
		share.ErrorCode = types.ErrIncorrectNonceSize // closest available: malformed/invalid submit
		return share
	}

	resultBig := result.Big()
	share.Diff = DifficultyFromResult(resultBig)

	shareTarget := TargetFromDifficulty(s.Difficulty())
	if !MeetsTarget(resultBig, shareTarget) {
		share.ErrorCode = types.ErrLowDiffShare
		return share
	}

	// block candidate?
	if MeetsTarget(resultBig, cur.target) {
		accepted, err := e.rpc.SubmitWork(nonceHex, cur.headerHash, mixDigest.Hex())
		if err != nil {
			log.Error("ethash eth_submitWork error: ", err)
		} else if accepted {
			share.BlockHash = cur.headerHash
			log.Warn("ethash block sealed at height ", cur.blockNumber, " header ", cur.headerHash)
		} else {
			log.Warn("ethash block rejected by node at height ", cur.blockNumber)
		}
	}

	return share
}

// --- helpers ---

func parseSubmit(params []interface{}) (nonce, headerHash, mixHash string, ok bool) {
	if len(params) < 3 {
		return "", "", "", false
	}
	n, ok1 := params[0].(string)
	h, ok2 := params[1].(string)
	m, ok3 := params[2].(string)
	if !ok1 || !ok2 || !ok3 {
		return "", "", "", false
	}
	return n, h, m, true
}

func hexToBig(s string) (*big.Int, bool) {
	return new(big.Int).SetString(strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X"), 16)
}


func shortJobID(headerHash string) string {
	h := strings.TrimPrefix(headerHash, "0x")
	if len(h) >= 8 {
		return h[:8]
	}
	return h
}

