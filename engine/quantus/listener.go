package quantus

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/quic-go/quic-go"
)

// Listen adapts the official miner's framed QUIC messages to the shared
// Stratum client lifecycle, authorization, share accounting and banning.
func (e *Engine) Listen(port int, opts *config.PortOptions) (net.Listener, error) {
	if err := validatePort(opts); err != nil {
		return nil, err
	}
	if opts.Protocol == "stratum" {
		return e.listenStratum(port, opts)
	}
	if opts == nil || opts.TLS == nil {
		return nil, errors.New("quantus: pool TLS required")
	}
	cert, err := tls.LoadX509KeyPair(opts.TLS.CertFile, opts.TLS.KeyFile)
	if err != nil {
		return nil, err
	}
	l, err := quic.ListenAddr(net.JoinHostPort("", strconv.Itoa(port)), &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, NextProtos: []string{alpn}}, &quic.Config{KeepAlivePeriod: 5 * time.Second, MaxIdleTimeout: 60 * time.Second, MaxIncomingStreams: 1, MaxIncomingUniStreams: -1})
	if err != nil {
		return nil, err
	}
	return &minerListener{Listener: l, engine: e}, nil
}

type minerListener struct {
	*quic.Listener
	engine *Engine
}

func (l *minerListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept(context.Background())
	if err != nil {
		return nil, err
	}
	// Return immediately; stream establishment occurs in the client's read
	// goroutine so a peer that never opens a stream cannot stall other miners.
	client := &minerConn{conn: c, engine: l.engine}
	l.engine.minersMu.Lock()
	l.engine.miners[client] = struct{}{}
	l.engine.minersMu.Unlock()
	return client, nil
}
func (l *minerListener) Addr() net.Addr { return tcpAddr(l.Listener.Addr()) }
func tcpAddr(a net.Addr) net.Addr {
	u := a.(*net.UDPAddr)
	return &net.TCPAddr{IP: u.IP, Port: u.Port, Zone: u.Zone}
}

type minerConn struct {
	engine                      *Engine
	writeBuf                    []byte
	conn                        quic.Connection
	mu                          sync.Mutex
	stream                      quic.Stream
	worker                      string
	readBuf                     []byte // read goroutine only
	job                         *request
	readDeadline, writeDeadline time.Time
}

func (c *minerConn) Read(p []byte) (int, error) {
	n, err := c.read(p)
	if err != nil {
		_ = c.Close()
		// The shared client releases its session on EOF. QUIC uses distinct
		// application/transport errors for disconnects, including peer closes.
		return n, io.EOF
	}
	return n, nil
}
func (c *minerConn) read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.readBuf) == 0 {
		c.mu.Lock()
		s := c.stream
		c.mu.Unlock()
		if s == nil {
			ctx, cancel := context.WithTimeout(c.conn.Context(), 10*time.Second)
			var err error
			s, err = c.conn.AcceptStream(ctx)
			cancel()
			if err != nil {
				return 0, err
			}
			c.mu.Lock()
			c.stream = s
			s.SetReadDeadline(time.Now().Add(10 * time.Second))
			c.mu.Unlock()
		}
		m, err := readMessage(s)
		if err != nil {
			return 0, err
		}
		var req interface{}
		if c.worker == "" {
			if m.Ready == nil || strings.TrimSpace(m.Ready.Token) == "" || len(m.Ready.Token) > 128 {
				return 0, errors.New("quantus: Ready token must be a worker name (1..128 bytes)")
			}
			c.worker = m.Ready.Token
			if c.engine.payments != nil {
				a, err := payoutAddress(c.worker)
				if err != nil {
					return 0, err
				}
				if a == c.engine.payoutSender {
					return 0, errors.New("signing wallet cannot be a mining payout address")
				}
			}
			c.mu.Lock()
			s.SetReadDeadline(c.readDeadline)
			c.mu.Unlock()
			req = map[string]interface{}{"id": 1, "method": "mining.authorize", "params": []interface{}{c.worker, ""}}
		} else {
			r := m.JobResult
			if r == nil || r.Status != "completed" || r.Work == nil {
				return 0, errors.New("quantus: expected completed JobResult with work")
			}
			req = map[string]interface{}{"id": 2, "method": "mining.submit", "params": []interface{}{c.worker, r.JobID, *r.Work}}
		}
		c.readBuf, err = json.Marshal(req)
		if err != nil {
			return 0, err
		}
		c.readBuf = append(c.readBuf, '\n')
	}
	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}
func (c *minerConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeBuf = append(c.writeBuf, p...)
	if len(c.writeBuf) > 16384 {
		return 0, errors.New("quantus: internal frame too large")
	}
	for {
		i := bytes.IndexByte(c.writeBuf, '\n')
		if i < 0 {
			break
		}
		line := c.writeBuf[:i]
		c.writeBuf = c.writeBuf[i+1:]
		if _, err := c.writeLine(line); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}
func (c *minerConn) writeLine(p []byte) (int, error) {
	var msg struct {
		ID     *int     `json:"id"`
		Method string   `json:"method"`
		Params []string `json:"params"`
	}
	if err := json.Unmarshal(p, &msg); err != nil {
		return 0, err
	}
	if c.stream == nil {
		return 0, io.ErrClosedPipe
	}
	if msg.Method == "mining.notify" {
		if len(msg.Params) != 3 {
			return 0, errors.New("quantus: invalid internal notification")
		}
		c.job = &request{JobID: msg.Params[0], MiningHash: msg.Params[1], Difficulty: msg.Params[2]}
	} else if msg.ID == nil || *msg.ID != 2 {
		return len(p), nil
	}
	// Official miners stop after finding a solution. Every submit response
	// (including a rejected share) starts another random nonce search.
	if c.job != nil {
		deadline := time.Now().Add(10 * time.Second)
		if !c.writeDeadline.IsZero() && c.writeDeadline.Before(deadline) {
			deadline = c.writeDeadline
		}
		c.stream.SetWriteDeadline(deadline)
		if err := writeMessage(c.stream, message{NewJob: c.job}); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}
func (c *minerConn) Close() error {
	c.engine.minersMu.Lock()
	delete(c.engine.miners, c)
	c.engine.minersMu.Unlock()
	return c.conn.CloseWithError(0, "")
}
func (c *minerConn) LocalAddr() net.Addr  { return tcpAddr(c.conn.LocalAddr()) }
func (c *minerConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }
func (c *minerConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}
func (c *minerConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline = t
	if c.stream != nil {
		return c.stream.SetReadDeadline(t)
	}
	return nil
}
func (c *minerConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeDeadline = t
	if c.stream != nil {
		return c.stream.SetWriteDeadline(t)
	}
	return nil
}
