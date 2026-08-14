# End-to-end testing

> 中文版：[E2E_zh.md](E2E_zh.md)

The whole pipeline is validated against **real coin nodes and real miners**
(`tools/e2eminer`, `tools/e2ervnminer`, `tools/e2exmrminer`, `tools/e2ekasminer`,
`tools/e2eethminer`):

```
node getblocktemplate/getWork → pool assembles → stratum → miner solves
  → submit → pool verifies PoW → block
```

## Scoreboard

**All 10 coins are green in the GitHub Actions `e2e` job** (linux/amd64 runner,
real daemon binaries downloaded, real blocks mined / verified) — see
[`.github/workflows/ci.yml`](../.github/workflows/ci.yml):

| Coin | Algorithm / engine | Node | Result |
|------|--------------------|------|--------|
| BTC | sha256d | bitcoind 27 regtest | ✅ **real block** (16→17) |
| LTC | scrypt | litecoind 0.21.4 regtest (2 nodes) | ✅ **real block** |
| DASH | x11 | dashd 23 regtest (2 nodes) | ✅ **real block** |
| GRS | groestl | groestlcoind 31 regtest | ✅ **real block** (single-sha256 merkle) |
| MONA | lyra2rev2 | monacoind 0.20.4 regtest (2 nodes) | ✅ **real block** |
| VTC | verthash | vertcoind 23.2 regtest | ✅ **real block** (pool `verthash.dat` pre-baked at image build) |
| RVN | **KawPow** engine | ravend 4.7.0 regtest (2 nodes) | ✅ **real block** (kawpow engine over the GBT node path) |
| ETC | **ethash** engine | core-geth 1.12.19 private chain | ✅ **real seal** (remote sealer + `eth_submitWork`) |
| XMR | **RandomX** engine | monerod 0.18 regtest | ✅ **real block** (1→2, pool re-computes RandomX) |
| KAS | **kHeavyHash** engine | kaspad 0.12.13 simnet | ✅ **real block** (pool verifies with kaspad's own `pow.State`) |

**Coverage:** 6 GBT algorithms (sha256d/scrypt/x11/groestl/lyra2rev2/verthash) +
4 engines (kawpow · ethash · RandomX · kHeavyHash), each mining a real block on an
isolated regtest/simnet node.

> **Assertion policy.** Each runner first checks for a real block (chain height
> grew / node sealed the block). If an isolated regtest/simnet node rejects a
> block the pool has already assembled and submitted (a node-side limitation), the
> runner falls back to *"pool validated through block submission"* — the pool
> independently re-computed the PoW with each coin's authoritative implementation,
> matched the miner, and called `submitblock`. In the last green run all 10 coins
> reached the real-block assertion.

## Reproduce (Docker, one command)

`scripts/e2e/` is a fully reproducible suite with no Homebrew dependency. The
supported path is the Docker image, which bundles every coin daemon
(regtest/simnet), Redis, and the Go toolchain (RandomX links a prebuilt lib
from the go-randomx module):

```bash
docker build -t nomp-e2e -f scripts/e2e/Dockerfile .
docker run --rm nomp-e2e                 # every coin, prints a scoreboard
docker run --rm nomp-e2e BTC GRS XMR     # a subset
```

Bare host (no Docker): put the GBT daemons on `PATH`
(`scripts/e2e/fetch-deps.sh` downloads them per OS/arch); the engine daemons
(`ravend`, `monerod` + `monero-wallet-rpc`, `kaspad` + `kaspawallet`, `geth` /
core-geth) must be on `PATH` too. Then run `scripts/e2e/run-all.sh`.

## Suite layout

- **`common.sh`** — shared helpers: start Redis, build the pool/miner with the
  right `-tags`, free ports, JSON-RPC calls, `with_timeout`.
- **`gbt.sh <NAME> <SYM> <daemon> <cli> <algo> <rpcport> <sport> [key=val …]`** —
  generic GBT coin E2E, with every quirk encoded: `peers=2` (older Bitcoin cores
  refuse GBT without a peer on regtest), `gbtRules=` (LTC needs `mweb`, DASH has
  no segwit), `coinbaseHasher=`/`blockHasher=` (GRS single-sha256 merkle), waiting
  for wall-clock to catch up to open the nTime window, `waitReady=` (VTC verthash
  datafile), `diff=` (kawpow needs a far lower share diff), and `engine=` — RVN
  runs here in `engine=kawpow` mode: the node is a GBT-family `ravend`, but the
  pool runs as a kawpow engine driven by `e2ervnminer`, reusing the same node
  bring-up.
- **`ethash.sh` / `cryptonote.sh` / `kaspa.sh`** — the three engine runners whose
  node interaction is not bitcoind-family: a core-geth private chain
  (`--mine --miner.threads 0`, remote sealer only, so only the pool can seal),
  monerod regtest (mints a valid pay-to address via `monero-wallet-rpc`), kaspad
  simnet (`kaspawallet` address, `KASPA_ALLOW_UNSYNCED=1`).
- **`run-all.sh`** — orchestrates all 10 coins (6 GBT + 4 engine) and prints a
  scoreboard; exits non-zero if any coin fails (with `E2E_STRICT=1`, a skipped
  coin also fails).

## GitHub Actions

Three gating jobs run on push / PR / manual dispatch:

| job | content | notes |
|-----|---------|-------|
| `test` | `go build ./…` · `go vet ./…` · `go test -short ./…` | default (no cgo), hermetic |
| `test-cgo` | build + `go test -short` with all tags (RandomX lib prebuilt) | covers every engine's compile + unit tests |
| `e2e` | build the Docker image and `docker run` (real block per coin) | heavy, `timeout-minutes: 90`, amd64 runner |

`-short` skips the `p2p` live-node test (needs a live node on `:19335`). Engine
daemons are downloaded per architecture; where an arch has no release asset (e.g.
kaspad/core-geth on arm64) that coin skips (⏭) rather than blocking the others.
