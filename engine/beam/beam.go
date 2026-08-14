// Package beam implements a pluggable engine.Engine for Beam (BeamHash III).
// Beam inverts the usual roles: the beam-node runs the stratum server, and the
// pool connects to it as a CLIENT over a newline-delimited JSON stream (TLS by
// default). The node pushes jobs and the pool forwards solutions.
//
// Node dialect (BeamMW/beam stratum API):
//
//	pool -> node  {"method":"login","api_key":"...","id":"login","jsonrpc":"2.0"}
//	node -> pool  {"method":"result","code":0,"nonceprefix":"ab4e3a","id":"login"}
//	node -> pool  {"method":"job","id":"212","input":"<64 hex>","difficulty":N}
//	pool -> node  {"method":"solution","id":"212","nonce":"<16 hex>","output":"<208 hex>"}
//	node -> pool  {"method":"result","code":1,"description":"accepted","id":"212"}
//
// PoW: BeamHash III over the 40-byte header (input(32) || nonce(8)) with the
// 104-byte solution, verified by powkit (pure Go, pinned by its Beam vector).
// The pool forwards every equihash-valid solution to the node, which performs
// the authoritative difficulty/target check.
package beam

import (
	"bufio"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/sencha-dev/powkit/beamhashiii"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/engine/worksource"
	"github.com/mining-pool/not-only-mining-pool/types"
)

var log = logging.Logger("beam")

func init() {
	engine.Register("beam", func() engine.Engine { return New() })
	engine.Register("beamhash", func() engine.Engine { return New() })
}

type job struct {
	id         string
	input      []byte // 32-byte pre-pow header
	difficulty uint64

	mu   sync.Mutex
	seen map[string]struct{}
}

// Engine is the Beam mining engine.
type Engine struct {
	opts    *config.Options
	pow     *beamhashiii.Client
	verify  func([]byte, []byte) (bool, error)
	noncePrefix []byte

	writeMu sync.Mutex
	enc     *json.Encoder
	conn    net.Conn

	mu      sync.RWMutex
	cur     *job
	history map[string]*job
}

func New() *Engine {
	c := beamhashiii.NewBeam()
	return &Engine{pow: c, verify: c.Verify, history: map[string]*job{}}
}

func (e *Engine) Name() string { return "beam" }

func (e *Engine) NotifyMethod() string { return "mining.notify" }

func (e *Engine) TargetParams(diff float64) (string, []interface{}) {
	return "mining.set_difficulty", []interface{}{diff}
}

func (e *Engine) Init(opts *config.Options) error {
	e.opts = opts
	if len(opts.Daemons) == 0 {
		return errors.New("beam: no daemon configured")
	}
	return e.connectAndLogin()
}

func (e *Engine) connectAndLogin() error {
	d := e.opts.Daemons[0]
	addr := fmt.Sprintf("%s:%d", d.Host, d.Port)

	var conn net.Conn
	var err error
	if d.TLS != nil {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr,
			&tls.Config{InsecureSkipVerify: true}) // beam-node uses a self-signed cert
	} else {
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
	}
	if err != nil {
		return err
	}

	e.conn = conn
	e.enc = json.NewEncoder(conn)
	return e.send(map[string]interface{}{
		"method": "login", "api_key": d.Password, "id": "login", "jsonrpc": "2.0",
	})
}

func (e *Engine) send(msg interface{}) error {
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	if e.enc == nil {
		return errors.New("beam: not connected")
	}
	return e.enc.Encode(msg) // json.Encoder appends '\n'
}

// Watch consumes the node's pushed job stream (event-driven by construction).
func (e *Engine) Watch(onNewWork func()) error {
	source := worksource.Subscribe("beam-node", func(onEvent func()) error {
		scanner := bufio.NewScanner(e.conn)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			if e.handleLine(scanner.Bytes()) {
				onEvent()
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		return errors.New("beam node closed the stream")
	}, func() (bool, error) { return true, nil })

	return worksource.Run(onNewWork, func(emit worksource.Emit) error {
		for {
			if e.conn == nil {
				if err := e.connectAndLogin(); err != nil {
					log.Warn("beam reconnect failed: ", err)
					time.Sleep(3 * time.Second)
					continue
				}
			}
			err := source(emit)
			log.Warn("beam stream ended: ", err)
			if e.conn != nil {
				_ = e.conn.Close()
			}
			e.conn, e.enc = nil, nil
			time.Sleep(time.Second)
		}
	})
}

// handleLine parses one node message; it returns true when a new job arrived.
func (e *Engine) handleLine(line []byte) bool {
	var m struct {
		Method      string `json:"method"`
		ID          string `json:"id"`
		Input       string `json:"input"`
		Difficulty  uint64 `json:"difficulty"`
		Code        int    `json:"code"`
		Description string `json:"description"`
		NoncePrefix string `json:"nonceprefix"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		log.Warn("beam bad line: ", string(line))
		return false
	}

	switch m.Method {
	case "job":
		input, err := hex.DecodeString(m.Input)
		if err != nil || len(input) != 32 {
			log.Warn("beam bad job input: ", m.Input)
			return false
		}
		j := &job{id: m.ID, input: input, difficulty: m.Difficulty, seen: map[string]struct{}{}}
		e.mu.Lock()
		e.cur = j
		e.history[j.id] = j
		if len(e.history) > 16 {
			for k := range e.history {
				if k != j.id && len(e.history) > 16 {
					delete(e.history, k)
				}
			}
		}
		e.mu.Unlock()
		log.Info("beam job ", m.ID, " difficulty ", m.Difficulty)
		return true
	case "result":
		if m.ID == "login" {
			if p, err := hex.DecodeString(m.NoncePrefix); err == nil {
				e.noncePrefix = p
			}
			log.Info("beam login: ", m.Description, " nonceprefix=", m.NoncePrefix)
		} else if m.Code == 1 {
			log.Warn("beam solution accepted for job ", m.ID)
		} else {
			log.Info("beam result job ", m.ID, ": ", m.Description)
		}
	}
	return false
}

func (e *Engine) currentJob() *job {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cur
}

func (e *Engine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	// forward the node-assigned nonce prefix so miners' nonces stay in range
	return []interface{}{nil, hex.EncodeToString(e.noncePrefix)}, e.noncePrefix, 8 - len(e.noncePrefix)
}

// JobParamsForDifficulty: [jobId, inputHex, difficulty].
func (e *Engine) JobParamsForDifficulty(_ float64) []interface{} {
	j := e.currentJob()
	if j == nil {
		return nil
	}
	return []interface{}{j.id, hex.EncodeToString(j.input), j.difficulty}
}

func (e *Engine) JobNotification(_ bool) (string, []interface{}) {
	return "mining.notify", e.JobParamsForDifficulty(1)
}

// OnSubmit validates [worker, jobId, nonceHex, outputHex] and forwards valid
// equihash solutions to the node.
func (e *Engine) OnSubmit(s engine.Session, params []interface{}) *types.Share {
	share := &types.Share{Miner: s.WorkerName(), RemoteAddr: s.RemoteAddr()}

	if len(params) < 4 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	jobID, ok1 := params[1].(string)
	nonceHex, ok2 := params[2].(string)
	outputHex, ok3 := params[3].(string)
	if !ok1 || !ok2 || !ok3 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	nonceHex = strings.ToLower(strings.TrimPrefix(nonceHex, "0x"))
	outputHex = strings.ToLower(strings.TrimPrefix(outputHex, "0x"))

	e.mu.RLock()
	j := e.history[jobID]
	e.mu.RUnlock()
	if j == nil {
		share.ErrorCode = types.ErrJobNotFound
		return share
	}
	share.JobId = j.id

	nonce, err := hex.DecodeString(nonceHex)
	if err != nil || len(nonce) != 8 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	output, err := hex.DecodeString(outputHex)
	if err != nil || len(output) != 104 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}

	j.mu.Lock()
	key := nonceHex + outputHex
	if _, dup := j.seen[key]; dup {
		j.mu.Unlock()
		share.ErrorCode = types.ErrDuplicateShare
		return share
	}
	j.seen[key] = struct{}{}
	j.mu.Unlock()

	// BeamHash III verifies over the 40-byte header = input(32) || nonce(8)
	header := append(append([]byte{}, j.input...), nonce...)
	valid, err := e.verify(header, output)
	if err != nil || !valid {
		share.ErrorCode = types.ErrIncorrectNonceSize // invalid solution
		return share
	}
	share.Diff = float64(j.difficulty)

	// forward to the node, which performs the authoritative target check
	if err := e.send(map[string]interface{}{
		"method": "solution", "id": j.id, "nonce": nonceHex, "output": outputHex, "jsonrpc": "2.0",
	}); err != nil {
		log.Error("beam forward solution failed: ", err)
	} else {
		share.BlockHash = hex.EncodeToString(header)
	}

	return share
}
