// Package quantus implements Quantus QPoW and its authenticated QUIC miner protocol.
package quantus

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/types"
	"github.com/quic-go/quic-go"
)

var log = logging.Logger("quantus")
var maxTarget = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1))

func init() { engine.Register("quantus", func() engine.Engine { return New() }) }

type job struct {
	seq uint64
	request
	header [32]byte
	target *big.Int
	seen   map[[64]byte]bool
}
type Engine struct {
	nonceCounter   atomic.Uint32
	jobCounter     uint64 // protected by mu
	minersMu       sync.Mutex
	miners         map[net.Conn]struct{}
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex // job lifecycle, duplicates and upstream writes are serialized
	cur            *job
	last           *job // preserve duplicate tracking when reconnecting to the same work
	conn           quic.Connection
	stream         quic.Stream
	address, token string
	tls            *tls.Config
	payments       *quantusPayments
	payoutSender   string
}

func New() *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{ctx: ctx, cancel: cancel, miners: make(map[net.Conn]struct{})}
}
func (e *Engine) Close() error {
	e.cancel()
	e.disconnectMiners()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cur = nil
	if e.conn != nil {
		return e.conn.CloseWithError(0, "")
	}
	return nil
}
func (e *Engine) Name() string         { return "quantus" }
func (e *Engine) NotifyMethod() string { return "mining.notify" }
func (e *Engine) Init(opts *config.Options) error {
	if opts.Quantus == nil {
		return errors.New("quantus: missing quantus configuration")
	}
	if err := validatePayments(opts); err != nil {
		return err
	}
	if len(opts.Ports) == 0 {
		return errors.New("quantus: configure at least one mining port")
	}
	for _, p := range opts.Ports {
		if err := validatePort(p); err != nil {
			return err
		}
	}
	q := opts.Quantus
	if q.NodeAddress == "" {
		return errors.New("quantus: missing nodeAddress")
	}
	token, err := os.ReadFile(q.AuthTokenFile)
	if err != nil {
		return fmt.Errorf("quantus: auth token file: %w", err)
	}
	e.token = strings.TrimSpace(string(token))
	if len(e.token) == 0 || len(e.token) > 512 {
		return errors.New("quantus: auth token must be 1..512 bytes")
	}
	pin, err := os.ReadFile(q.TLSCertSHA256File)
	if err != nil {
		return fmt.Errorf("quantus: fingerprint file: %w", err)
	}
	e.tls, err = pinnedTLS(string(pin))
	if err != nil {
		return err
	}
	e.address = q.NodeAddress
	return e.connect()
}
func validDiff(d float64) bool {
	if d < 1 || math.IsInf(d, 0) || math.IsNaN(d) {
		return false
	}
	n, _ := new(big.Float).SetFloat64(d).Int(nil)
	return n.BitLen() <= 512
}

func (e *Engine) disconnectMiners() {
	e.minersMu.Lock()
	clients := make([]net.Conn, 0, len(e.miners))
	for c := range e.miners {
		clients = append(clients, c)
	}
	e.minersMu.Unlock()
	for _, c := range clients {
		_ = c.Close()
	}
}
func parseJob(r *request) (*job, error) {
	if r == nil || r.JobID == "" || len(r.JobID) > 128 {
		return nil, errors.New("quantus: invalid job ID")
	}
	b, err := hex.DecodeString(r.MiningHash)
	if err != nil || len(b) != 32 {
		return nil, errors.New("quantus: invalid mining hash")
	}
	d, ok := new(big.Int).SetString(r.Difficulty, 10)
	if !ok || d.Sign() <= 0 || d.BitLen() > 512 || strings.Trim(r.Difficulty, "0123456789") != "" {
		return nil, errors.New("quantus: invalid difficulty")
	}
	j := &job{request: *r, target: new(big.Int).Div(new(big.Int).Set(maxTarget), d), seen: make(map[[64]byte]bool)}
	copy(j.header[:], b)
	return j, nil
}
func (e *Engine) connect() error {
	ctx, cancel := context.WithTimeout(e.ctx, 15*time.Second)
	defer cancel()
	c, err := quic.DialAddr(ctx, e.address, e.tls, &quic.Config{KeepAlivePeriod: 5 * time.Second, MaxIdleTimeout: 60 * time.Second})
	if err != nil {
		return err
	}
	s, err := c.OpenStreamSync(ctx)
	if err != nil {
		c.CloseWithError(0, "")
		return err
	}
	s.SetWriteDeadline(time.Now().Add(10 * time.Second))
	ready := message{}
	ready.Ready = &struct {
		Token string `json:"token"`
	}{e.token}
	if err = writeMessage(s, ready); err != nil {
		c.CloseWithError(0, "")
		return err
	}
	s.SetWriteDeadline(time.Time{})
	e.mu.Lock()
	if e.ctx.Err() != nil {
		e.mu.Unlock()
		c.CloseWithError(0, "")
		return e.ctx.Err()
	}
	e.conn = c
	e.stream = s
	e.mu.Unlock()
	// Nodes can be syncing or gated on tip freshness: no initial-job timeout.
	// Watch receives the first job; new miners wait until it arrives.
	return nil
}
func (e *Engine) Watch(onNewWork func()) error {
	delay := time.Second
	for {
		e.mu.Lock()
		s := e.stream
		e.mu.Unlock()
		m, err := readMessage(s)
		if e.ctx.Err() != nil {
			return nil
		}
		if err == nil {
			j, parseErr := parseJob(m.NewJob)
			if parseErr != nil {
				log.Warn(parseErr)
				e.mu.Lock()
				e.cur = nil
				e.mu.Unlock()
				e.disconnectMiners()
				continue
			}
			e.mu.Lock()
			// Repeated delivery of an identical job must not erase duplicate tracking.
			if e.last != nil && e.last.request == j.request {
				j = e.last
			} else {
				e.jobCounter++
				j.seq = e.jobCounter
			}
			e.cur, e.last = j, j
			e.mu.Unlock()
			delay = time.Second
			onNewWork()
			continue
		}
		e.mu.Lock()
		e.cur = nil
		e.conn.CloseWithError(0, "")
		e.mu.Unlock()
		e.disconnectMiners()
		onNewWork()
		log.Warn("node connection lost; reconnecting: ", err)
		for {
			select {
			case <-e.ctx.Done():
				return nil
			case <-time.After(delay):
			}
			if err = e.connect(); err == nil {
				break
			}
			log.Warn("node reconnect failed: ", err)
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}
func (e *Engine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	return true, nil, 0
}
func (e *Engine) JobParamsForDifficulty(diff float64) []interface{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur == nil || !validDiff(diff) {
		return nil
	}
	d, _ := new(big.Float).SetFloat64(math.Ceil(diff)).Int(nil)
	return []interface{}{e.cur.JobID, e.cur.MiningHash, d.String()}
}
func (e *Engine) JobNotification(_ bool) (string, []interface{}) {
	return e.NotifyMethod(), e.JobParamsForDifficulty(1)
}
func (e *Engine) OnSubmit(s engine.Session, p []interface{}) *types.Share {
	share := &types.Share{Miner: s.WorkerName(), RemoteAddr: s.RemoteAddr()}
	if len(p) != 3 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	id, ok := p[1].(string)
	nonceHex, ok2 := p[2].(string)
	b, err := hex.DecodeString(nonceHex)
	if !ok || !ok2 || err != nil || len(b) != 64 {
		share.ErrorCode = types.ErrIncorrectNonceSize
		return share
	}
	var nonce [64]byte
	copy(nonce[:], b)
	e.mu.Lock()
	defer e.mu.Unlock()
	j := e.cur
	if j == nil || j.JobID != id {
		share.ErrorCode = types.ErrJobNotFound
		return share
	}
	share.JobId = id
	if j.seen[nonce] {
		share.ErrorCode = types.ErrDuplicateShare
		return share
	}
	hash := NonceHash(j.header, nonce)
	value := new(big.Int).SetBytes(hash[:])
	share.Diff = engine.DiffFromValue(maxTarget, value)
	if value.Sign() == 0 {
		share.Diff = math.MaxFloat64
	}
	if !validDiff(s.Difficulty()) {
		share.ErrorCode = types.ErrLowDiffShare
		return share
	}
	// Fixed integer share difficulty and strict comparison, as in qpow-math.
	d, _ := new(big.Float).SetFloat64(math.Ceil(s.Difficulty())).Int(nil)
	target := new(big.Int).Div(new(big.Int).Set(maxTarget), d)
	lowShare := value.Cmp(target) >= 0
	if lowShare && value.Cmp(j.target) >= 0 {
		share.ErrorCode = types.ErrLowDiffShare
		return share
	}
	if e.payments != nil {
		if err := e.payments.record(s.WorkerName(), j.MiningHash, hex.EncodeToString(nonce[:]), d, !lowShare, value.Cmp(j.target) < 0); err != nil {
			log.Error("Quantus share accounting failed: ", err)
			share.ErrorCode = types.ErrJobNotFound
			return share
		}
	}
	if value.Cmp(j.target) < 0 {
		work := hex.EncodeToString(nonce[:])
		n := new(big.Int).SetBytes(nonce[:]).Text(16)
		e.stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
		err := writeMessage(e.stream, message{JobResult: &result{Status: "completed", JobID: id, Nonce: &n, Work: &work}})
		e.stream.SetWriteDeadline(time.Time{})
		if err != nil {
			// Leave the nonce retryable and do not report a successful upstream write.
			share.ErrorCode = types.ErrJobNotFound
			log.Error("solution write failed: ", err)
			return share
		}
		// The protocol has no acceptance receipt or final block hash. Do not mark
		// a submitted seal as a confirmed block or invoke Bitcoin payout machinery.
		log.Warn("submitted Quantus solution for job ", id, " (node acceptance unconfirmed)")
	}
	j.seen[nonce] = true
	if lowShare {
		share.ErrorCode = types.ErrLowDiffShare
	}
	return share
}
