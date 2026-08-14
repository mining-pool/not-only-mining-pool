# Pluggable mining engines

> 中文版（含逐引擎实现细节）：[PLUGGABLE_ENGINES_zh.md](PLUGGABLE_ENGINES_zh.md)

This document explains how coins with a non-Bitcoin mining model (ETC, XMR, RVN,
KAS, ZEC/Flux, ERG, BEAM, ALPH) plug into the pool, reusing everything except node
interaction.

## The key distinction

For **header-hash coins** (sha256d/scrypt/x11/keccak/groestl/lyra2rev2/verthash…)
"reuse everything, swap only node interaction" already holds: node interaction is
just config (`daemons` host/port/user/pass), and the only per-coin difference — the
header hash — is pluggable via `algorithm.RegisterHash`.

For the other coins the difference is **not** node interaction; it is the **mining
model itself** — block-header structure, PoW verification, block assembly, and the
**Stratum dialect** (subscribe/notify/submit message shapes). A "node response
parser" is not enough; what's needed is a pluggable **job engine**.

## Reuse boundary

```
┌───────────────── shared base (reused by every engine, unchanged) ─────────────────┐
│ stratum TCP server · client lifecycle · vardiff · banning · redis storage · payments · API │
└───────────────────────────────────────────────────────────────────────────────────────────┘
            ▲  Session interface (ExtraNonce1 / Difficulty / WorkerName / Send …)
            │
┌───────────┴──────────────── engine.Engine (per coin) ─────────────────────────────┐
│  Init(opts)        connect to the node, fetch first work                            │
│  Watch(onNewWork)  event-first work source (poll only as fallback)                  │
│  OnSubscribe       mining.subscribe dialect (extranonce assignment)                 │
│  JobNotification   notify message (method + params, dialect-specific)               │
│  OnSubmit          verify submission, check PoW, submit block if it meets target    │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

The interface is in [`engine/engine.go`](../engine/engine.go). Optional
capabilities are expressed as small optional interfaces the stratum layer checks
for (`diffJobber`, `notifyMethoder`, `targetSetter`, `objectParamser`) so a dialect
that needs, say, a `mining.set_target` before `mining.notify`, or object-shaped
params, does not force changes on the others.

Two pool constructors branch on the config `"engine"` field: `NewPool` (the
Bitcoin/GBT path) and `NewEnginePool` (engine mode, which wires up only the shared
base). The existing Bitcoin path is effectively an implicit GBT engine and serves
as the reference implementation.

## Engine status

Every engine below is **end-to-end validated on a real node** in CI — see
[E2E.md](E2E.md). "Pure Go" engines are in the default build; heavy ones are behind
build tags so the default binary stays lean.

### KawPow — Ravencoin (`engine/kawpow/`, pure Go, default build)

Reuses the full GBT machinery (`daemons.DaemonManager` + `jobs.NewJob`: coinbase,
merkle, target, txdata). KawPow rolls a 64-bit header nonce rather than the
coinbase, so the coinbase extranonce is fixed per job. Byte order was verified
against Ravencoin's `block.cpp`/`hash.cpp`. PoW uses powkit's light cache (~16MB
per 7500-block epoch, warmed asynchronously at Init). Dialect (kawpowminer/t-rex):
`mining.notify [jobId, headerHash, seedHash, shareTarget, clean, height, bits]`,
`mining.submit [worker, jobId, nonce, headerHash, mixHash]`.

### Ethash — Ethereum Classic (`engine/ethash/`, `-tags ethash`)

Node-builds-blocks model: the pool does **not** assemble blocks. `eth_getWork`
returns `[headerHash, seedHash, target]`; the miner returns `nonce + mixHash`; the
pool verifies with `go-etchash` (light cache, ECIP-1099 aware) and relays via
`eth_submitWork`. Dialect: ethproxy (`eth_submitLogin`/`eth_getWork`/
`eth_submitWork`). No coinbase/merkle/header-serialization is used.

### RandomX — Monero / CryptoNote (`engine/cryptonote/`, `-tags randomx`)

Single chain + JSON-RPC; the node fills in the coinbase and leaves only a
`reserved_offset` for the pool. The hard parts, both solved: a RandomX cgo binding
(`third_party/go-randomx`, RandomX v1.2.1, light-mode VM so verification needs only
~256MB cache) and a faithful Go port of Monero's `tree_hash` (cross-checked 16/16
against the official C source). Dialect: XMRig — object-shaped `login`/`submit`
params, `login` doubles as subscribe+authorize and carries the first job. Build
needs `librandomx.a` (`cd third_party/go-randomx && ./build.sh`).

### kHeavyHash — Kaspa (`engine/kaspa/`, `-tags kaspa`)

The biggest departure. Three challenges, all solved without touching other engines:
(1) **streaming work** — Kaspa produces 1–10 blocks/s, so `Watch()` uses kaspad's
gRPC new-template notification stream with a slow poll as backup; (2) **official
consensus code** — block-header hashing depends on typed protobuf structures, so the
engine depends on `github.com/kaspanet/kaspad` (rpcclient + consensus/pow) rather
than reimplementing it (heavy → build-tag isolated); (3) a dependency-conflict fix
(go-redis/otel). PoW correctness is cross-verified in a unit test: kaspad's official
`pow.State` and powkit's independent heavyhash must agree for the same header.
Dialect: kaspa-stratum-bridge convention.

### Equihash — Zcash / Flux (`engine/equihash/`, pure Go, default build)

Variant-parameterized: `"equihash"` (Zcash 200,9) / `"zelhash"` (Flux 125,4). The
node's GBT already provides the full `coinbasetxn` (with funding streams), so the
pool builds no coinbase. 140-byte Zcash header, str4d zcash stratum dialect
(`mining.set_target` before `mining.notify`). `OnSubmit` is pinned to powkit's real
Flux block vector.

### Autolykos2 — Ergo (`engine/ergo/`, pure Go, default build)

Node-builds-blocks, simplest of all: `GET /mining/candidate → {msg, b(target), h}`,
the pool rolls the nonce, `POST /mining/solution`. PoW via powkit autolykos2, pinned
to the official Ergo vector. Handles targets larger than int64 via `json.Number`.

### BeamHash III — Beam · Blake3 — Alephium (`engine/beam/`, `engine/alephium/`, pure Go)

Both needed a bespoke transport, not a new engine interface. **Beam**: the node is
the stratum server, so the pool is a **client** over a line-delimited TLS-JSON
connection (`worksource.Subscribe`, auto-reconnect). **Alephium**: a custom binary
protocol (`uint32BE(len)‖version‖type‖payload`) with `blockflow` emitting one job
per chain; the engine keeps the whole job set and routes submissions by jobId — all
engine-internal, no interface change. PoW = `blake3(blake3(nonce‖header))`.

## Event-first work source (`engine/worksource/`)

"How an engine learns about new work" is factored into its own component; events are
first-class, polling is only a fallback. `Watch(onNewWork)` is backed by:

- `Poll(interval, refresh)` — polling source / fallback.
- `Subscribe(name, open, refresh)` — event source; `open` blocks consuming the
  subscription and fires `refresh` per event, auto-reconnecting.
- `ZMQSource(name, endpoint, topic, refresh)` — a ZMQ SUB event source
  (`github.com/go-zeromq/zmq4`, pure Go).
- `Run(emit, sources…)` — races multiple sources concurrently (event source +
  slow poll backup).

Per-engine sources: KAS (kaspad gRPC notifications), XMR/RVN/ZEC/FLUX (node ZMQ
`hashblock` / chain-main when `daemons[].zmq` is configured), ETC/ERG (poll). Add
`"zmq": "tcp://127.0.0.1:28332"` to a `daemons[]` entry (and `-zmqpubhashblock` on
bitcoind / `--zmq-pub` on monerod) to enable events; leave it empty for pure poll.

## Summary

- **Header-hash coins:** no new architecture — config (+ a hash binding) is enough.
- **Engine coins:** the pluggable point is the **mining model** (job engine +
  Stratum dialect), not a node-response parser. All of the above engines are
  implemented, unit-tested against authoritative PoW vectors, and end-to-end
  validated on real nodes in CI.
