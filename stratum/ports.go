package stratum

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/mining-pool/not-only-mining-pool/config"
)

// ListenProvider is implemented by engines that bind their own listeners
// (e.g. Quantus QUIC). getCert serves the port's current certificate so a
// renewal applies to new handshakes without rebinding.
type ListenProvider interface {
	Listen(port int, opts *config.PortOptions, getCert func(*tls.ClientHelloInfo) (*tls.Certificate, error)) (net.Listener, error)
}

// portListener is one bound stratum port. opts is replaced atomically when a
// reload changes settings that do not require rebinding; connections capture
// the value in force when they are accepted.
type portListener struct {
	port   int
	opts   atomic.Pointer[config.PortOptions]
	certs  *config.CertificateHolder // nil without TLS
	ln     net.Listener
	closed atomic.Bool
}

// PortChange describes what a reload did to one port.
type PortChange struct {
	Port   int    `json:"port"`
	Action string `json:"action"` // added, removed, updated, rebound
}

func (c PortChange) String() string { return fmt.Sprintf("port %d %s", c.Port, c.Action) }

// needsRebind reports whether moving from old to next settings needs a new
// listener: switching TLS on/off or the protocol changes the socket itself.
func needsRebind(old, next *config.PortOptions) bool {
	return (old.TLS == nil) != (next.TLS == nil) || old.Protocol != next.Protocol
}

func (ss *Server) bind(port int, opts *config.PortOptions) (*portListener, error) {
	pl := &portListener{port: port}
	pl.opts.Store(opts)
	if opts.TLS != nil {
		holder, err := config.NewCertificateHolder(opts.TLS)
		if err != nil {
			return nil, err
		}
		pl.certs = holder
	}
	var getCert func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	if pl.certs != nil {
		getCert = pl.certs.GetCertificate
	}
	var err error
	address := ":" + strconv.Itoa(port)
	if provider, ok := ss.Engine.(ListenProvider); ok {
		pl.ln, err = provider.Listen(port, opts, getCert)
	} else if getCert != nil {
		pl.ln, err = tls.Listen("tcp", address, &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: getCert})
	} else {
		pl.ln, err = net.Listen("tcp", address)
	}
	if err != nil {
		return nil, fmt.Errorf("port %d: %w", port, err)
	}
	return pl, nil
}

// serve accepts connections until the listener is closed.
func (ss *Server) serve(pl *portListener) {
	go func() {
		for {
			conn, err := pl.ln.Accept()
			if err != nil {
				if !pl.closed.Load() {
					log.Error("port ", pl.port, " stopped accepting: ", err)
				}
				return
			}
			if conn != nil {
				log.Info("new conn from ", conn.RemoteAddr().String(), " on port ", pl.port)
				go ss.handleNewClient(conn, pl.port, pl.opts.Load())
			}
		}
	}()
}

func (pl *portListener) close() {
	pl.closed.Store(true)
	_ = pl.ln.Close()
}

// startPorts binds every configured port at startup.
func (ss *Server) startPorts(ports map[int]*config.PortOptions) []int {
	ss.portsMu.Lock()
	defer ss.portsMu.Unlock()
	if ss.listeners == nil {
		ss.listeners = make(map[int]*portListener)
	}
	var started []int
	for port, opts := range ports {
		if opts == nil {
			continue
		}
		pl, err := ss.bind(port, opts)
		if err != nil {
			log.Error(err)
			continue
		}
		ss.listeners[port] = pl
		ss.serve(pl)
		started = append(started, port)
	}
	sort.Ints(started)
	return started
}

// Ports returns the ports currently being served.
func (ss *Server) Ports() []int {
	ss.portsMu.Lock()
	defer ss.portsMu.Unlock()
	ports := make([]int, 0, len(ss.listeners))
	for port := range ss.listeners {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

// portOptions returns the settings a new connection on port would get.
func (ss *Server) portOptions(port int) *config.PortOptions {
	ss.portsMu.Lock()
	defer ss.portsMu.Unlock()
	if pl := ss.listeners[port]; pl != nil {
		return pl.opts.Load()
	}
	if ss.Options != nil {
		return ss.Options.Ports[port]
	}
	return nil
}

// PreparedPorts is a validated port change. Commit applies it; Abort releases
// anything Prepare bound.
type PreparedPorts struct {
	ss      *Server
	next    map[int]*config.PortOptions
	added   map[int]*portListener
	certs   map[int]*tls.Certificate
	rebind  []int
	updated []int
	removed []int
	done    bool
}

// PreparePorts validates next against the running ports without changing
// anything visible: certificates are loaded and new ports are bound (but not
// yet served). Any error leaves the server untouched.
func (ss *Server) PreparePorts(next map[int]*config.PortOptions) (*PreparedPorts, error) {
	ss.portsMu.Lock()
	defer ss.portsMu.Unlock()
	p := &PreparedPorts{ss: ss, next: next, added: map[int]*portListener{}, certs: map[int]*tls.Certificate{}}
	fail := func(err error) (*PreparedPorts, error) {
		for _, pl := range p.added {
			pl.close()
		}
		return nil, err
	}
	if len(next) == 0 {
		return fail(errors.New("at least one stratum port is required"))
	}
	if v, ok := ss.Engine.(interface {
		ValidatePort(*config.PortOptions) error
	}); ok {
		for port, opts := range next {
			if err := v.ValidatePort(opts); err != nil {
				return fail(fmt.Errorf("port %d: %w", port, err))
			}
		}
	}
	for port, opts := range next {
		if opts == nil {
			return fail(fmt.Errorf("port %d: missing settings", port))
		}
		if opts.VarDiff != nil && (opts.VarDiff.TargetTime <= 0 || opts.VarDiff.RetargetTime <= 0 || opts.VarDiff.MinDiff <= 0 || opts.VarDiff.MaxDiff < opts.VarDiff.MinDiff) {
			return fail(fmt.Errorf("port %d: varDiff needs positive targetTime, retargetTime, minDiff and maxDiff >= minDiff", port))
		}
		if opts.Diff <= 0 {
			return fail(fmt.Errorf("port %d: diff must be positive", port))
		}
		current := ss.listeners[port]
		switch {
		case current == nil:
			pl, err := ss.bind(port, opts)
			if err != nil {
				return fail(err)
			}
			p.added[port] = pl
		case needsRebind(current.opts.Load(), opts):
			if opts.TLS != nil {
				if _, err := opts.TLS.LoadCertificate(); err != nil {
					return fail(fmt.Errorf("port %d: %w", port, err))
				}
			}
			p.rebind = append(p.rebind, port)
		default:
			if opts.TLS != nil {
				// Reload even when the paths are unchanged to pick up renewals.
				cert, err := opts.TLS.LoadCertificate()
				if err != nil {
					return fail(fmt.Errorf("port %d: %w", port, err))
				}
				p.certs[port] = cert
			}
			if !reflect.DeepEqual(current.opts.Load(), opts) {
				p.updated = append(p.updated, port)
			}
		}
	}
	for port := range ss.listeners {
		if _, ok := next[port]; !ok {
			p.removed = append(p.removed, port)
		}
	}
	return p, nil
}

// Abort releases ports bound by PreparePorts.
func (p *PreparedPorts) Abort() {
	if p == nil || p.done {
		return
	}
	p.done = true
	for _, pl := range p.added {
		pl.close()
	}
}

// Commit applies the prepared change. Connected miners keep the settings they
// connected with; miners on removed (or rebound) ports are disconnected so they
// reconnect according to their own failover configuration.
func (p *PreparedPorts) Commit() (changes []PortChange, errs []error) {
	if p.done {
		return nil, []error{errors.New("port change already applied")}
	}
	p.done = true
	ss := p.ss
	ss.portsMu.Lock()
	for port, pl := range p.added {
		ss.listeners[port] = pl
		ss.serve(pl)
		changes = append(changes, PortChange{port, "added"})
	}
	for port, cert := range p.certs {
		ss.listeners[port].certs.Set(cert)
	}
	for _, port := range p.updated {
		ss.listeners[port].opts.Store(p.next[port])
		changes = append(changes, PortChange{port, "updated"})
	}
	drop := map[int]bool{}
	for _, port := range p.removed {
		ss.listeners[port].close()
		delete(ss.listeners, port)
		drop[port] = true
		changes = append(changes, PortChange{port, "removed"})
	}
	for _, port := range p.rebind {
		ss.listeners[port].close()
		delete(ss.listeners, port)
		drop[port] = true
		pl, err := ss.bind(port, p.next[port])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ss.listeners[port] = pl
		ss.serve(pl)
		changes = append(changes, PortChange{port, "rebound"})
	}
	ss.portsMu.Unlock()

	if len(drop) > 0 {
		for _, c := range ss.snapshotClients() {
			if drop[c.Port] {
				log.Warn("disconnecting ", c.GetLabel(), ": port ", c.Port, " was removed or rebound by a configuration reload")
				c.closeSocket()
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Port < changes[j].Port })
	return changes, errs
}

// portsMu guards listeners; declared here to keep the reload state together.
type portState struct {
	portsMu   sync.Mutex
	listeners map[int]*portListener
}
