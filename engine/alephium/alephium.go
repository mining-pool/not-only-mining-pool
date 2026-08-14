// Package alephium implements a pluggable engine.Engine for Alephium (Blake3,
// blockflow sharding). The node exposes a binary miner API on TCP (default
// 10973): length-prefixed messages carrying, per notification, one Job PER
// CHAIN (groups×groups chains). The pool keeps the full job set and routes a
// submission back to the chain it belongs to.
//
// Wire format (github.com/alephium/alephium flow/.../mining/Message.scala):
//
//	frame        = uint32BE(len) || body
//	ServerMsg    = version(1) || msgType(1) || payload
//	Jobs(0x00)   = int32BE(count) || Job*count
//	Job          = fromGroup(4) || toGroup(4) || blob(headerBlob) ||
//	               blob(txsBlob) || blob(target) || int32BE(height)
//	blob(x)      = int32BE(len) || x
//	ClientMsg    = version(1) || msgType(1) || payload
//	SubmitBlock(0x00) = blob(blockBlob)  where blockBlob = nonce(24) ||
//	                    headerBlob || txsBlob
//
// PoW (protocol/.../mining/PoW.scala): hash = blake3(blake3(nonce||headerBlob)),
// valid when BigInt(1, hash) < target. Blake3 via lukechampine.com/blake3.
package alephium

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"lukechampine.com/blake3"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/engine/worksource"
	"github.com/mining-pool/not-only-mining-pool/types"
)

var log = logging.Logger("alephium")

const nonceSize = 24


func init() {
	engine.Register("alephium", func() engine.Engine { return New() })
	engine.Register("blake3", func() engine.Engine { return New() })
}

type job struct {
	id         string
	fromGroup  uint32
	toGroup    uint32
	headerBlob []byte
	txsBlob    []byte
	target     *big.Int
	height     int32

	mu   sync.Mutex
	seen map[string]struct{}
}

// Engine is the Alephium mining engine.
type Engine struct {
	opts       *config.Options
	conn       net.Conn
	writeMu    sync.Mutex
	jobCounter uint64
	enCounter  uint32

	mu      sync.RWMutex
	jobs    map[string]*job // jobId -> job (full current set, one per chain)
	order   []string        // jobIds in receive order (for a stable "current")
	history map[string]*job // recent jobs for late submits
}

func New() *Engine {
	return &Engine{jobs: map[string]*job{}, history: map[string]*job{}}
}

func (e *Engine) Name() string { return "alephium" }

func (e *Engine) NotifyMethod() string { return "mining.notify" }

func (e *Engine) Init(opts *config.Options) error {
	e.opts = opts
	if len(opts.Daemons) == 0 {
		return errors.New("alephium: no daemon configured")
	}
	if err := e.dial(); err != nil {
		return err
	}
	// the node pushes an initial Jobs message on connect; read it so the first
	// job is ready before miners connect.
	_, msgType, payload, err := readFrame(e.conn)
	if err != nil {
		return err
	}
	if msgType != msgJobs {
		return fmt.Errorf("alephium: expected initial Jobs, got type %d", msgType)
	}
	return e.handleJobs(payload)
}

func (e *Engine) dial() error {
	d := e.opts.Daemons[0]
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", d.Host, d.Port), 10*time.Second)
	if err != nil {
		return err
	}
	e.conn = conn
	return nil
}

// Watch reads the node's binary stream; each Jobs message is an event.
func (e *Engine) Watch(onNewWork func()) error {
	source := worksource.Subscribe("alephium-node", func(onEvent func()) error {
		for {
			_, msgType, payload, err := readFrame(e.conn)
			if err != nil {
				return err
			}
			switch msgType {
			case msgJobs:
				if err := e.handleJobs(payload); err != nil {
					log.Warn("alephium bad Jobs message: ", err)
					continue
				}
				onEvent()
			case msgSubmitResult:
				logSubmitResult(payload)
			default:
				log.Debug("alephium ignoring server msg type ", msgType)
			}
		}
	}, func() (bool, error) { return true, nil })
	// reconnect handled by Subscribe; re-dial before each attempt
	return worksource.Run(onNewWork, func(emit worksource.Emit) error {
		for {
			if e.conn == nil {
				if err := e.dial(); err != nil {
					log.Warn("alephium redial failed: ", err)
					time.Sleep(3 * time.Second)
					continue
				}
			}
			err := source(emit)
			log.Warn("alephium stream ended: ", err)
			_ = e.conn.Close()
			e.conn = nil
			time.Sleep(time.Second)
		}
	})
}

// handleJobs replaces the current job set with the chains in a Jobs payload.
func (e *Engine) handleJobs(payload []byte) error {
	jobs, err := parseJobs(payload, func() string {
		return strconv.FormatUint(atomic.AddUint64(&e.jobCounter, 1), 16)
	})
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.jobs = map[string]*job{}
	e.order = e.order[:0]
	for _, j := range jobs {
		e.jobs[j.id] = j
		e.order = append(e.order, j.id)
		e.history[j.id] = j
	}
	for len(e.history) > 64 {
		for k := range e.history {
			if _, live := e.jobs[k]; !live {
				delete(e.history, k)
				break
			}
		}
	}
	e.mu.Unlock()
	log.Info("alephium jobs updated: ", len(jobs), " chains")
	return nil
}

func (e *Engine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	en := make([]byte, 2)
	binary.BigEndian.PutUint16(en, uint16(atomic.AddUint32(&e.enCounter, 1)))
	return []interface{}{nil, hex.EncodeToString(en)}, en, nonceSize - 2
}

// JobParamsForDifficulty returns every current chain job so the miner can work
// all chains: [ [jobId, fromGroup, toGroup, headerBlobHex, targetHex], ... ].
func (e *Engine) JobParamsForDifficulty(diff float64) []interface{} {
	e.mu.RLock()
	defer e.mu.RUnlock()
	shareTarget := engine.TargetFromDiff(engine.Pow256, diff)
	out := make([]interface{}, 0, len(e.order))
	for _, id := range e.order {
		j := e.jobs[id]
		out = append(out, []interface{}{
			j.id,
			j.fromGroup,
			j.toGroup,
			hex.EncodeToString(j.headerBlob),
			engine.TargetHex(minBig(j.target, shareTarget)),
		})
	}
	return []interface{}{out}
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

	nonce, err := hex.DecodeString(nonceHex)
	if err != nil || len(nonce) != nonceSize {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	if en1 := s.ExtraNonce1(); len(en1) > 0 && !hasPrefixBytes(nonce, en1) {
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

	powHash := doubleBlake3(concat(nonce, j.headerBlob))
	hashNum := new(big.Int).SetBytes(powHash) // BigInt(1, hash): big-endian, positive
	share.Diff = engine.DiffFromValue(engine.Pow256, hashNum)

	if hashNum.Cmp(engine.TargetFromDiff(engine.Pow256, s.Difficulty())) > 0 {
		share.ErrorCode = types.ErrLowDiffShare
		return share
	}

	if hashNum.Cmp(j.target) <= 0 {
		blockBlob := concat(nonce, j.headerBlob, j.txsBlob)
		if err := e.submitBlock(blockBlob); err != nil {
			log.Error("alephium submit block failed: ", err)
		} else {
			share.BlockHash = hex.EncodeToString(powHash)
			log.Warn("alephium block submitted, chain ", j.fromGroup, "->", j.toGroup, " height ", j.height)
		}
	}

	return share
}

func (e *Engine) submitBlock(blockBlob []byte) error {
	payload := appendBlob(nil, blockBlob)
	frame := buildFrame(msgSubmitBlock, payload)
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	if e.conn == nil {
		return errors.New("alephium: not connected")
	}
	_, err := e.conn.Write(frame)
	return err
}

// --- PoW ---

func doubleBlake3(b []byte) []byte {
	h1 := blake3.Sum256(b)
	h2 := blake3.Sum256(h1[:])
	return h2[:]
}

// --- codec ---

const (
	msgJobs         byte = 0x00 // server -> client
	msgSubmitResult byte = 0x01 // server -> client
	msgSubmitBlock  byte = 0x00 // client -> server
	protocolVersion byte = 0x01
)

// readFrame reads one length-prefixed [version][type][payload] message.
func readFrame(conn net.Conn) (version, msgType byte, payload []byte, err error) {
	var lenBuf [4]byte
	if _, err = io.ReadFull(conn, lenBuf[:]); err != nil {
		return 0, 0, nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n < 2 || n > 32<<20 {
		return 0, 0, nil, fmt.Errorf("alephium: bad frame length %d", n)
	}
	body := make([]byte, n)
	if _, err = io.ReadFull(conn, body); err != nil {
		return 0, 0, nil, err
	}
	return body[0], body[1], body[2:], nil
}

// buildFrame serializes [len][version][type][payload].
func buildFrame(msgType byte, payload []byte) []byte {
	body := make([]byte, 0, 2+len(payload))
	body = append(body, protocolVersion, msgType)
	body = append(body, payload...)
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out, uint32(len(body)))
	copy(out[4:], body)
	return out
}

func parseJobs(payload []byte, nextID func() string) ([]*job, error) {
	r := &reader{b: payload}
	count := r.int32()
	if r.err != nil || count < 0 || count > 4096 {
		return nil, errors.New("alephium: bad job count")
	}
	jobs := make([]*job, 0, count)
	for i := int32(0); i < count; i++ {
		j := &job{id: nextID(), seen: map[string]struct{}{}}
		j.fromGroup = uint32(r.int32())
		j.toGroup = uint32(r.int32())
		j.headerBlob = r.blob()
		j.txsBlob = r.blob()
		j.target = new(big.Int).SetBytes(r.blob())
		j.height = r.int32()
		if r.err != nil {
			return nil, r.err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

type reader struct {
	b   []byte
	pos int
	err error
}

func (r *reader) int32() int32 {
	if r.err != nil || r.pos+4 > len(r.b) {
		r.err = io.ErrUnexpectedEOF
		return 0
	}
	v := int32(binary.BigEndian.Uint32(r.b[r.pos:]))
	r.pos += 4
	return v
}

func (r *reader) blob() []byte {
	n := r.int32()
	if r.err != nil || n < 0 || r.pos+int(n) > len(r.b) {
		r.err = io.ErrUnexpectedEOF
		return nil
	}
	v := r.b[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return v
}

func appendBlob(dst, x []byte) []byte {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(x)))
	dst = append(dst, l[:]...)
	return append(dst, x...)
}

func logSubmitResult(payload []byte) {
	if len(payload) > 0 && payload[0] != 0 {
		log.Warn("alephium node accepted a block")
	} else {
		log.Info("alephium submit result: ", hex.EncodeToString(payload))
	}
}

// --- helpers ---




func minBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) < 0 {
		return a
	}
	return b
}

func concat(parts ...[]byte) []byte {
	var n int
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func hasPrefixBytes(b, prefix []byte) bool {
	if len(prefix) > len(b) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}
