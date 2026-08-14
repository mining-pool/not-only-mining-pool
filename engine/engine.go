// Package engine defines the pluggable mining-engine seam that lets this pool
// serve coins whose mining MODEL (not just node RPC) differs from Bitcoin's
// getblocktemplate flow — e.g. KawPow (Ravencoin), Ethash/Etchash (ETC),
// Equihash (Zcash), RandomX (Monero).
//
// The insight behind this package:
//
//   - For sha256d/scrypt/x11/keccak-style coins the ONLY thing that changes
//     between coins is node config + the header hash function. Those are already
//     pluggable (see config.DaemonOptions and algorithm.RegisterHash); no engine
//     is needed.
//
//   - For the "C-class" coins the divergence is NOT node interaction, it is the
//     whole mining model: block-header structure, PoW verification signature,
//     block assembly, and the stratum dialect (subscribe/notify/submit). A
//     response parser is not enough — you need a pluggable JOB ENGINE.
//
// An Engine owns exactly the coin-model-specific behaviour. Everything else is
// shared infrastructure that engines reuse verbatim:
//
//	shared (reused by every engine):  stratum TCP server, client lifecycle,
//	                                  vardiff, banning, redis storage, payments, API
//	engine-specific (this interface): node work source, job construction,
//	                                  PoW verification, block submission,
//	                                  stratum subscribe/notify/submit dialect
//
// The existing Bitcoin flow (daemons + jobs + the bitcoin branch of
// stratum.Client) is the REFERENCE implementation; extracting it into a
// gbt.Engine that satisfies this interface is step 1 of any engine work and is
// a pure refactor (no behaviour change).
package engine

import (
	"net"

	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/types"
)

// Session is the minimal per-connection surface a stratum client exposes to an
// engine. It is an interface so the engine package does not depend on the
// stratum package (the stratum client implements it), avoiding an import cycle.
type Session interface {
	// ExtraNonce1 is the per-connection nonce prefix assigned at subscribe time
	// (bitcoin/kawpow use it inside the coinbase; ethash-style engines ignore it).
	ExtraNonce1() []byte
	// Difficulty is the current vardiff-adjusted share difficulty for this client.
	Difficulty() float64
	// WorkerName is the authorized worker (usually the payout address).
	WorkerName() string
	// RemoteAddr is the miner's address, for share accounting and banning.
	RemoteAddr() net.Addr
	// Send pushes a stratum notification to the miner. method is the dialect's
	// notify method (e.g. "mining.notify", "mining.set_target", "eth_notify").
	Send(method string, params []interface{}) error
}

// Engine encapsulates everything coin-model-specific. Implementations are
// expected to be safe for concurrent use: OnSubscribe/OnSubmit are called from
// per-connection goroutines while Poll runs in its own goroutine.
type Engine interface {
	// Name is the engine identifier matched against config's "engine" field
	// (e.g. "gbt", "kawpow", "ethash").
	Name() string

	// Init wires the engine to its node(s) and any shared services it needs.
	// It should perform the initial work fetch so the first job is ready.
	Init(opts *config.Options) error

	// OnSubscribe handles mining.subscribe for a new client and returns the
	// dialect-specific reply plus the extranonce assignment. Engines that do not
	// use extranonces (ethash-style) return a nil/empty extraNonce1 and size 0.
	OnSubscribe(s Session, params []interface{}) (result interface{}, extraNonce1 []byte, extraNonce2Size int)

	// JobNotification returns the current job's notify method + params, ready to
	// hand to Session.Send. clean signals miners to drop stale work.
	JobNotification(clean bool) (method string, params []interface{})

	// OnSubmit validates a miner submission. If it solves a block the engine
	// submits it to the node and fills Share.BlockHash/BlockHex/BlockHeight.
	// Share.ErrorCode is set for rejected submissions.
	OnSubmit(s Session, params []interface{}) *types.Share

	// Watch is a long-running loop that watches the node for new work. It calls
	// onNewWork whenever the shared layer must re-broadcast a job to all miners.
	// Engines are EVENT-DRIVEN where the node offers a push API (gRPC/ZMQ/WS/p2p
	// notifications) and fall back to polling only otherwise — see the
	// worksource package. It returns only on a fatal error.
	Watch(onNewWork func()) error
}

// Factory builds a fresh Engine instance.
type Factory func() Engine

var registry = map[string]Factory{}

// Register makes an engine available under the given name (matched
// case-sensitively against config's "engine"). Call it from an engine package's
// init():
//
//	func init() { engine.Register("kawpow", func() engine.Engine { return &Engine{} }) }
func Register(name string, f Factory) {
	registry[name] = f
}

// Get returns a new engine instance for name, or (nil, false) if unregistered.
func Get(name string) (Engine, bool) {
	f, ok := registry[name]
	if !ok {
		return nil, false
	}
	return f(), true
}

// Registered returns the names of all registered engines.
func Registered() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
