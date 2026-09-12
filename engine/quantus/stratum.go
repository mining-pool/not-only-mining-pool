package quantus

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net"
	"strconv"
	"strings"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/types"
)

func validatePort(p *config.PortOptions) error {
	if p == nil {
		return errors.New("quantus: missing mining port options")
	}
	if p.Protocol != "" && p.Protocol != "quic" && p.Protocol != "stratum" {
		return errors.New("quantus: port protocol must be quic or stratum")
	}
	if p.Protocol != "stratum" && p.TLS == nil {
		return errors.New("quantus: QUIC mining port requires TLS certFile and keyFile")
	}
	if p.TLS != nil {
		if _, err := tls.LoadX509KeyPair(p.TLS.CertFile, p.TLS.KeyFile); err != nil {
			return fmt.Errorf("quantus: pool TLS: %w", err)
		}
	}
	if !validDiff(p.Diff) || p.Diff != math.Trunc(p.Diff) || p.VarDiff != nil {
		return errors.New("quantus: use finite integer port diff >= 1 and varDiff=null")
	}
	// LuckyPool's JSON job difficulty is a uint64 (the QUIC protocol uses U512 strings).
	if p.Protocol == "stratum" && p.Diff >= math.Exp2(64) {
		return errors.New("quantus: Stratum difficulty must fit uint64")
	}
	return nil
}

// ClientEngine chooses the miner dialect per listener while all clients share
// the same node work, duplicate set, verifier, and solution submission path.
func (e *Engine) ClientEngine(_ int, p *config.PortOptions) engine.Engine {
	if p != nil && p.Protocol == "stratum" {
		return &stratumEngine{Engine: e, diff: p.Diff}
	}
	return e
}

func (e *Engine) listenStratum(port int, p *config.PortOptions) (net.Listener, error) {
	address := net.JoinHostPort("", strconv.Itoa(port))
	var l net.Listener
	var err error
	if p.TLS != nil {
		cert, certErr := tls.LoadX509KeyPair(p.TLS.CertFile, p.TLS.KeyFile)
		if certErr != nil {
			return nil, certErr
		}
		l, err = tls.Listen("tcp", address, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}})
	} else {
		l, err = net.Listen("tcp", address)
	}
	if err != nil {
		return nil, err
	}
	return &stratumListener{Listener: l, engine: e}, nil
}

type stratumListener struct {
	net.Listener
	engine *Engine
}

func (l *stratumListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &stratumConn{Conn: c, engine: l.engine}
	l.engine.minersMu.Lock()
	l.engine.miners[wrapped] = struct{}{}
	l.engine.minersMu.Unlock()
	return wrapped, nil
}

type stratumConn struct {
	net.Conn
	engine *Engine
}

func (c *stratumConn) Close() error {
	c.engine.minersMu.Lock()
	delete(c.engine.miners, c)
	c.engine.minersMu.Unlock()
	return c.Conn.Close()
}

// stratumEngine speaks LuckyPool's Quantus dialect, which is JSON-RPC with
// login/job/submit objects, not Bitcoin's mining.subscribe/mining.notify arrays.
type stratumEngine struct {
	*Engine
	diff float64
}

func (e *stratumEngine) NotifyMethod() string        { return "job" }
func (e *stratumEngine) ObjectParams() bool          { return true }
func (e *stratumEngine) NotificationVersion() string { return "2.0" }
func (e *stratumEngine) ValidateLogin(params []interface{}) error {
	if len(params) != 1 {
		return errors.New("expected login object")
	}
	obj, ok := params[0].(map[string]interface{})
	if !ok {
		return errors.New("expected login object")
	}
	login, ok := obj["login"].(string)
	if !ok || strings.TrimSpace(login) == "" || len(login) > 128 {
		return errors.New("login must be an account.worker name (1..128 bytes)")
	}
	if e.payments != nil {
		a, err := payoutAddress(login)
		if err != nil {
			return err
		}
		if a == e.payoutSender {
			return errors.New("signing wallet cannot be a mining payout address")
		}
	}
	return nil
}
func (e *stratumEngine) OnSubscribe(_ engine.Session, _ []interface{}) (interface{}, []byte, int) {
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, e.nonceCounter.Add(1))
	return map[string]interface{}{
		"id": hex.EncodeToString(prefix), "status": "OK", "extensions": []string{"keepalive"},
		"job": e.minerJob(prefix, e.diff),
	}, prefix, 60
}
func (e *stratumEngine) minerJob(prefix []byte, diff float64) interface{} {
	if len(prefix) != 4 || !validDiff(diff) {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	j := e.cur
	if j == nil {
		return nil
	}
	d, _ := new(big.Float).SetFloat64(diff).Int(nil)
	target := new(big.Int).Div(new(big.Int).Set(maxTarget), d)
	return map[string]interface{}{
		"algo": "qpow-poseidon2", "job_id": j.JobID, "mining_hash": j.MiningHash,
		"extranonce": hex.EncodeToString(prefix), "target": fmt.Sprintf("%0128x", target),
		"difficulty": json.Number(d.String()), "seq": j.seq,
	}
}
func (e *stratumEngine) JobParamsForSession(s engine.Session) []interface{} {
	j := e.minerJob(s.ExtraNonce1(), e.diff)
	if j == nil {
		return nil
	}
	return []interface{}{map[string]interface{}{"clean_jobs": true, "job": j}}
}
func (e *stratumEngine) OnSubmit(s engine.Session, params []interface{}) *types.Share {
	reject := func() *types.Share {
		return &types.Share{Miner: s.WorkerName(), RemoteAddr: s.RemoteAddr(), ErrorCode: types.ErrIncorrectNonceSize}
	}
	if len(params) != 1 {
		return reject()
	}
	obj, ok := params[0].(map[string]interface{})
	if !ok {
		return reject()
	}
	id, ok := obj["id"].(string)
	if !ok || len(s.ExtraNonce1()) != 4 || id != hex.EncodeToString(s.ExtraNonce1()) {
		return reject()
	}
	nonce, ok := obj["nonce"].(string)
	if !ok || len(nonce) != 128 || !strings.HasPrefix(strings.ToLower(nonce), hex.EncodeToString(s.ExtraNonce1())) {
		return reject()
	}
	jobID, ok := obj["job_id"].(string)
	if !ok {
		return reject()
	}
	return e.Engine.OnSubmit(s, []interface{}{s.WorkerName(), jobID, nonce})
}
