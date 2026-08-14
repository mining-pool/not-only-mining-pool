# Not-Only-Mining-Pool (NOMP)

A standalone, high-performance Stratum mining-pool server written in Go.

It started as a pool for **Bitcoin Core (`bitcoind`) variants** — any coin whose
proof-of-work is a hash of the standard 80-byte block header — and now also ships
**pluggable mining engines** for coins with entirely different mining models
(Ethash, RandomX, KawPow, kHeavyHash, Equihash, Autolykos2, BeamHash III, Blake3).

> Docs: [Tutorial](TUTORIAL.md) · [Pluggable engines](docs/PLUGGABLE_ENGINES.md) ·
> [End-to-end testing](docs/E2E.md)
> · 中文：[教程](TUTORIAL_zh.md) · [引擎](docs/PLUGGABLE_ENGINES_zh.md) · [E2E](docs/E2E_zh.md)

## Why standalone?

Unlike the original NOMP (node-open-mining-portal), this is **not a portal** — it
is just the Stratum server. Keeping it standalone makes it easy to add a new
algorithm or coin without fighting C-library conflicts or restarting an entire
site. Most operators don't need a portal; they run a handful of coins across a
few algorithms. If you want a web front-end, build one against the API.

## What it supports

### GBT coins (default build, no build tags)

The pool requests `getblocktemplate`, assembles the coinbase, computes the merkle
root, serializes the 80-byte header, hashes it, and submits solved blocks via
`submitblock`. The only thing that changes between these coins is the header hash
function.

Built-in algorithms: `sha256`, `sha256d`, `scrypt`, `x11`, `keccak`, `groestl`,
`lyra2rev2`, `verthash`, plus `neoscrypt` (cgo). More can be registered via
`algorithm.RegisterHash` — see the [tutorial](TUTORIAL_zh.md). Ready-to-use coin
templates live in [`coins/`](coins/).

### Pluggable engines (opt-in via build tags)

For coins whose block structure, PoW verification and Stratum dialect differ from
Bitcoin, a pluggable `engine.Engine` reuses everything except node interaction
(Stratum server, vardiff, banning, storage, payments, API). See
[docs/PLUGGABLE_ENGINES.md](docs/PLUGGABLE_ENGINES.md).

| Engine | Coin(s) | Build tag | Notes |
|--------|---------|-----------|-------|
| `kawpow` | Ravencoin | *(default)* | pure Go (powkit); GBT node path |
| `equihash` / `zelhash` | Zcash / Flux | *(default)* | pure Go (powkit blake2b) |
| `ergo` | Ergo (Autolykos2) | *(default)* | pure Go (powkit + REST) |
| `beam` | Beam (BeamHash III) | *(default)* | pure Go; TLS-JSON client transport |
| `alephium` | Alephium (Blake3) | *(default)* | pure Go; binary protocol, multi-chain |
| `ethash` | Ethereum Classic … | `-tags ethash` | go-etchash + go-ethereum |
| `randomx` | Monero (CryptoNote) | `-tags randomx` | cgo, needs `librandomx.a` |
| `kaspa` | Kaspa (kHeavyHash) | `-tags kaspa` | kaspad gRPC + consensus |

The default binary stays lean; heavy dependencies are gated behind build tags.
Selecting an engine in the config without its build tag fails loudly rather than
silently running as Bitcoin.

## Build

```bash
# default (GBT coins + pure-Go engines)
go build .

# with selected engines
CGO_ENABLED=1 go build -tags "ethash kaspa" .

# RandomX needs its static lib first (once):
cd third_party/go-randomx && ./build.sh && cd -
CGO_ENABLED=1 go build -tags randomx .
```

## Configure & run

Copy `config.example.json` to `config.json`, edit the `daemons`, `poolAddress`,
`ports` and `storage` (Redis) sections, then:

```bash
./not-only-mining-pool -c config.json
```

Engine coins add an `"engine"` field (e.g. `"engine": "ethash"`); GBT coins omit
it (or use `"gbt"`). Per-engine example configs live under `engine/<name>/`.

## Testing

- **Unit tests** (hermetic): `go test -short ./...`, and with every engine tag:
  `CGO_ENABLED=1 go test -short -tags "neoscrypt ethash randomx kaspa kawpow" ./...`
- **End-to-end** (real nodes, regtest/simnet): a reproducible Docker suite mines a
  real block for every coin. All 10 coins pass in CI. See
  [docs/E2E.md](docs/E2E.md).

```bash
docker build -t nomp-e2e -f scripts/e2e/Dockerfile .
docker run --rm nomp-e2e            # every coin
docker run --rm nomp-e2e BTC XMR    # a subset
```

## Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs three gating jobs on
every push and PR:

| job | what it does |
|-----|--------------|
| `test` | default build · vet · `go test -short` |
| `test-cgo` | build `librandomx.a`, then build + test all engine tags |
| `e2e` | build the Docker image and mine a real block per coin |

## Documentation

| Topic | English | 中文 |
|-------|---------|------|
| Adapt mainstream coins / add an algorithm | [TUTORIAL.md](TUTORIAL.md) | [TUTORIAL_zh.md](TUTORIAL_zh.md) |
| Pluggable engines (architecture + status) | [docs/PLUGGABLE_ENGINES.md](docs/PLUGGABLE_ENGINES.md) | [docs/PLUGGABLE_ENGINES_zh.md](docs/PLUGGABLE_ENGINES_zh.md) |
| End-to-end testing | [docs/E2E.md](docs/E2E.md) | [docs/E2E_zh.md](docs/E2E_zh.md) |

## TODO

- Engine-mode share persistence to Redis (block submission already works; this is
  for stats/payments — see the TODO in `stratum/engine.go`)
- More algorithms
- Web front-end

## Donation

**LTC**: `LXxqHY4StG79nqRurdNNt1wF2yCf4Mc986`
