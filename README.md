# Not-Only-Mining-Pool (NOMP)

A standalone, high-performance Stratum mining-pool server written in Go.

It started as a pool for **Bitcoin Core (`bitcoind`) variants** — any coin whose
proof-of-work is a hash of the standard 80-byte block header — and now also ships
**pluggable mining engines** for coins with entirely different mining models
(Ethash, RandomX, KawPow, kHeavyHash, Equihash, Autolykos2, BeamHash III, Blake3,
Poseidon2).

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

### Pluggable engines

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
| `quantus` | Quantus (QTC / Poseidon2) | *(default)* | QUIC + LuckyPool TCP/TLS; fixed difficulty; finalized PROP/SOLO payouts |
| `ethash` | Ethereum Classic … | `-tags ethash` | go-etchash + go-ethereum |
| `randomx` | Monero (CryptoNote) | `-tags randomx` | cgo; prebuilt lib in go-randomx |
| `kaspa` | Kaspa (kHeavyHash) | `-tags kaspa` | kaspad gRPC + consensus |

The default binary stays lean; heavy dependencies are gated behind build tags.
Selecting an engine in the config without its build tag fails loudly rather than
silently running as Bitcoin.

## Build

```bash
# default (GBT coins + pure-Go engines)
go build ./cmd/nomp

# with selected engines (RandomX links a prebuilt lib shipped in
# github.com/mining-pool/go-randomx — no manual build step)
CGO_ENABLED=1 go build -tags "ethash kaspa randomx" ./cmd/nomp
```

## Configure & run

Copy `config.example.json` to `config.json`, edit the `daemons`, `poolAddress`,
`ports` and `storage` (Redis) sections, then:

```bash
./nomp -c config.json
```

Engine coins add an `"engine"` field (e.g. `"engine": "ethash"`); GBT coins omit
it (or use `"gbt"`). Per-engine example configs live under `engine/<name>/`.

For Quantus, see the [configuration and miner instructions](engine/quantus/README.md).
It supports the official miner and SRBMiner-Multi through native QUIC and
LuckyPool Quantus Stratum over TCP/TLS.

### Quantus mining and payments

Start with [`config.quantus.example.json`](engine/quantus/config.quantus.example.json).
Configure the node's miner endpoint, authentication token and certificate pin,
the pool's TLS certificate, and Redis. Mining is included in the default Go build;
ports use fixed integer share difficulty.

Optional **PROP/SOLO payments** settle finalized mining rewards in exact base
units (1 QTC = 10^12 units). Signed transactions are persisted before broadcast,
and finalized receipts prevent duplicate debits after a restart. `GET /payments`
exposes balances, pending transaction hashes and finalized receipts.

Payments require the [signing bridge setup](engine/quantus/README.md#自动支付)
(Node.js 20+, Rust and the official Quantus signing library), a dedicated funded
hot wallet, and Redis with `appendonly yes` and `appendfsync always`.
Wormhole mining rewards must still be collected with the official Quantus CLI;
automatic wormhole collection and Quantus PPS/PPLNS are not implemented.

## Testing

- **Unit tests** (hermetic): `go test -short ./...`, and with every engine tag:
  `CGO_ENABLED=1 go test -short -tags "neoscrypt ethash randomx kaspa kawpow" ./...`
- **End-to-end** (real nodes, regtest/simnet): a reproducible Docker suite mines a
  real block for every coin. All 10 coins pass in CI. See
  [docs/E2E.md](docs/E2E.md).
- **Quantus development-chain E2E** (separate local runner): tested with
  quantus-node 1.0.1 and the official quantus-miner 4.2.0 CPU engine. The run
  confirmed **128 pool blocks**, paid **1.23 QTC**, and recovered an unconfirmed
  transaction after a pool crash without a duplicate debit. See the
  [validation report](docs/QUANTUS_E2E.md). SRBMiner GPU binaries and production
  throughput have not been tested.

```bash
docker build -t nomp-e2e -f scripts/e2e/Dockerfile .
docker run --rm nomp-e2e            # every coin
docker run --rm nomp-e2e BTC XMR    # a subset

# Quantus, after installing its signing bridge and node/miner binaries
python3 scripts/e2e/quantus.py \
  --node /path/to/quantus-node --miner /path/to/quantus-miner
```

## Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs three gating jobs on
every push and PR:

| job | what it does |
|-----|--------------|
| `test` | default build · vet · `go test -short` |
| `test-cgo` | build + test all engine tags (RandomX lib prebuilt in the module) |
| `e2e` | build the Docker image and mine a real block per coin |

## Documentation

| Topic | English | 中文 |
|-------|---------|------|
| Adapt mainstream coins / add an algorithm | [TUTORIAL.md](TUTORIAL.md) | [TUTORIAL_zh.md](TUTORIAL_zh.md) |
| Pluggable engines (architecture + status) | [docs/PLUGGABLE_ENGINES.md](docs/PLUGGABLE_ENGINES.md) | [docs/PLUGGABLE_ENGINES_zh.md](docs/PLUGGABLE_ENGINES_zh.md) |
| End-to-end testing | [docs/E2E.md](docs/E2E.md) | [docs/E2E_zh.md](docs/E2E_zh.md) |

Quantus: [setup and payment configuration](engine/quantus/README.md) ·
[development-chain test report](docs/QUANTUS_E2E.md).

## TODO

- More algorithms
- Web front-end

## Donation

**LTC**: `LXxqHY4StG79nqRurdNNt1wF2yCf4Mc986`
