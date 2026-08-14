// Package ergo implements a pluggable engine.Engine for Ergo (Autolykos2). It
// is the cheapest of the "node-builds-the-block" engines: the node exposes a
// REST API and does everything except roll the nonce.
//
//	GET  /mining/candidate -> {msg(32B hex), b(target, decimal), h(height)}
//	POST /mining/solution  <- {pk, w, n(nonceHex), d}   (pool sends n)
//
// PoW: powkit autolykos2 (pure Go, pinned by the Ergo test vector). A solution
// is valid when Compute(msg, height, nonce) < b.
//
// Miner dialect (node-mining stratum used by lolMiner/nbminer/T-Rex for ERG):
//
//	mining.subscribe  -> [null, extraNonce1Hex]
//	mining.set_target / set_difficulty pushed with each job
//	mining.notify     -> [jobId, height, msgHex, "", cleanJobs]
//	mining.submit     -> [worker, jobId, nonceHex]
//
// The engine only needs REST (no gRPC / no consensus lib), so it is pure Go and
// built into the default binary.
package ergo

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/sencha-dev/powkit/autolykos2"

	"github.com/mining-pool/not-only-mining-pool/engine/worksource"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/types"
)

var log = logging.Logger("ergo")

func init() {
	engine.Register("ergo", func() engine.Engine { return New() })
	engine.Register("autolykos2", func() engine.Engine { return New() })
}

const nonce1Size = 2 // pool-assigned prefix bytes of the 8-byte nonce

type job struct {
	id     string
	msg    []byte // 32-byte candidate message
	height uint64
	target *big.Int // network target b

	mu   sync.Mutex
	seen map[string]struct{}
}

// Engine is the Ergo mining engine.
type Engine struct {
	opts         *config.Options
	rest         *ergoREST
	pow          *autolykos2.Client
	pollInterval time.Duration
	enCounter    uint32
	jobCounter   uint64

	mu      sync.RWMutex
	cur     *job
	history map[string]*job
}

func New() *Engine {
	return &Engine{pollInterval: time.Second, pow: autolykos2.NewErgo(), history: map[string]*job{}}
}

func (e *Engine) Name() string { return "ergo" }

func (e *Engine) NotifyMethod() string { return "mining.notify" }

func (e *Engine) TargetParams(diff float64) (string, []interface{}) {
	return "mining.set_target", []interface{}{engine.TargetHex(engine.TargetFromDiff(engine.Pow256, diff))}
}

func (e *Engine) Init(opts *config.Options) error {
	e.opts = opts
	if len(opts.Daemons) == 0 {
		return errors.New("ergo: no daemon configured")
	}
	d := opts.Daemons[0]
	e.rest = newErgoREST(fmt.Sprintf("http://%s:%d", d.Host, d.Port), d.Password)

	cand, err := e.rest.Candidate()
	if err != nil {
		return err
	}
	return e.acceptCandidate(cand)
}

func (e *Engine) acceptCandidate(c *candidate) error {
	msg, err := hex.DecodeString(c.Msg)
	if err != nil || len(msg) != 32 {
		return errors.New("ergo: bad candidate msg")
	}
	target, ok := new(big.Int).SetString(c.B, 10)
	if !ok {
		return errors.New("ergo: bad candidate target b")
	}

	id := strconv.FormatUint(atomic.AddUint64(&e.jobCounter, 1), 16)
	j := &job{id: id, msg: msg, height: c.H, target: target, seen: map[string]struct{}{}}

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
	log.Info("new ergo job ", id, " height ", c.H)
	return nil
}

func (e *Engine) currentJob() *job {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cur
}

// Watch polls the Ergo node's REST candidate endpoint (it exposes no push API).
func (e *Engine) Watch(onNewWork func()) error {
	return worksource.Run(onNewWork, worksource.Poll(e.pollInterval, e.refresh))
}

func (e *Engine) refresh() (bool, error) {
	cand, err := e.rest.Candidate()
	if err != nil {
		return false, err
	}
	if cur := e.currentJob(); cur != nil && bytes.Equal(cur.msg, mustHex(cand.Msg)) {
		return false, nil
	}
	if err := e.acceptCandidate(cand); err != nil {
		return false, err
	}
	return true, nil
}

// OnSubscribe assigns a 2-byte nonce prefix.
func (e *Engine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	en := make([]byte, nonce1Size)
	n := atomic.AddUint32(&e.enCounter, 1)
	en[0], en[1] = byte(n>>8), byte(n)
	return []interface{}{nil, hex.EncodeToString(en)}, en, 8 - nonce1Size
}

func (e *Engine) JobParamsForDifficulty(_ float64) []interface{} {
	j := e.currentJob()
	if j == nil {
		return nil
	}
	return []interface{}{j.id, j.height, hex.EncodeToString(j.msg), "", true}
}

func (e *Engine) JobNotification(_ bool) (string, []interface{}) {
	return "mining.notify", e.JobParamsForDifficulty(1)
}

// OnSubmit validates [worker, jobId, nonceHex].
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
	share.BlockHeight = int64(j.height)

	if len(nonceHex) != 16 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
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

	result, err := e.pow.Compute(j.msg, j.height, nonce)
	if err != nil {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	resultNum := new(big.Int).SetBytes(result)
	share.Diff = engine.DiffFromValue(engine.Pow256, resultNum)

	if resultNum.Cmp(engine.TargetFromDiff(engine.Pow256, s.Difficulty())) > 0 {
		share.ErrorCode = types.ErrLowDiffShare
		return share
	}

	// network solution?
	if resultNum.Cmp(j.target) <= 0 {
		if err := e.rest.SubmitNonce(nonceHex); err != nil {
			log.Error("ergo submit solution failed: ", err)
		} else {
			share.BlockHash = hex.EncodeToString(result)
			log.Warn("ergo solution accepted at height ", j.height)
		}
	}

	return share
}

// --- REST client ---

type ergoREST struct {
	base   string
	apiKey string
	client *http.Client
}

func newErgoREST(base, apiKey string) *ergoREST {
	return &ergoREST{base: strings.TrimRight(base, "/"), apiKey: apiKey, client: &http.Client{Timeout: 10 * time.Second}}
}

type candidate struct {
	Msg string `json:"msg"`
	B   string `json:"b"` // target; Ergo serializes it as a JSON number, decoded below as json.Number
	H   uint64 `json:"h"`
	PK  string `json:"pk"`
}

func (r *ergoREST) do(method, path string, body []byte, out interface{}) error {
	req, err := http.NewRequest(method, r.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("api_key", r.apiKey)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (r *ergoREST) Candidate() (*candidate, error) {
	// b is a JSON number that can exceed int64; decode leniently
	var raw struct {
		Msg string      `json:"msg"`
		B   json.Number `json:"b"`
		H   uint64      `json:"h"`
		PK  string      `json:"pk"`
	}
	if err := r.do(http.MethodGet, "/mining/candidate", nil, &raw); err != nil {
		return nil, err
	}
	return &candidate{Msg: raw.Msg, B: raw.B.String(), H: raw.H, PK: raw.PK}, nil
}

func (r *ergoREST) SubmitNonce(nonceHex string) error {
	body, _ := json.Marshal(map[string]string{"n": nonceHex})
	return r.do(http.MethodPost, "/mining/solution", body, nil)
}

// --- math helpers ---




func mustHex(s string) []byte {
	b, _ := hex.DecodeString(s)
	return b
}
