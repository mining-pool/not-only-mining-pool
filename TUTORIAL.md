# Adapting coins — tutorial

> 中文版（含完整 Top-20 币种归类表）：[TUTORIAL_zh.md](TUTORIAL_zh.md)

How to run mainstream PoW coins on this pool, and which coins fit which path.

## The adaptation boundary in one minute

The pool is a **Bitcoin `getblocktemplate` (GBT) Stratum server**: it asks the
node for `getblocktemplate`, assembles the coinbase, computes the merkle root,
serializes the standard 80-byte header (`jobs/job.go` `SerializeHeader`), hashes it
to check share difficulty, and submits blocks via `submitblock`. **The only thing
that changes between GBT coins is the header hash function.**

So coins fall into three categories:

| Category | Description | Cost |
|----------|-------------|------|
| **A. header-hash, built-in** | PoW = f(80-byte header): sha256d / scrypt / x11 / keccak / groestl / lyra2rev2 / verthash | config only |
| **B. header-hash, needs binding** | same, but no pure-Go impl → cgo binding to a C lib (e.g. neoscrypt) | one `RegisterHash` file + a build tag |
| **C. different mining model** | header structure / mining model differ: Ethash, RandomX, Equihash, KawPow, kHeavyHash, Autolykos2, BeamHash III, Blake3 | a pluggable **engine** — already provided, see below |

For the full Top-20 classification with per-coin config templates, see
[TUTORIAL_zh.md](TUTORIAL_zh.md) and [`coins/`](coins/).

## Quick start (category A, 5 steps)

Using **Litecoin**:

```bash
# 1) copy a template to the repo root
cp coins/config.litecoin.json config.json

# 2) edit config.json:
#    - poolAddress.address / rewardRecipients[].address → your wallet address(es)
#    - daemons[0] host/port/user/password → your litecoind RPC
#    - storage → your redis

# 3) litecoind must expose RPC (litecoin.conf):
#      server=1
#      rpcuser=rpcuser
#      rpcpassword=rpcpassword
#      rpcallowip=127.0.0.1

# 4) build
go build ./cmd/nomp

# 5) run
./not-only-mining-pool -c config.json -l info
```

Miners connect to `stratum+tcp://<your-ip>:3032` with the wallet address as the
username.

## Config field cheatsheet

| Field | Meaning | Notes |
|-------|---------|-------|
| `coin.name` / `coin.symbol` | display / storage key | |
| `algorithm.name` | algorithm | must be a registered algorithm (below) |
| `algorithm.multiplier` | difficulty multiplier (2^n scale of diff-1) | **leave 0** → filled from the algorithm default |
| `algorithm.sha256dBlockHasher` | block id uses sha256d | `true` for scrypt/sha256d; `false` for x11/keccak etc. (use the algorithm itself) |
| `algorithm.blockHasher` | explicit block-id algorithm (overrides above) | e.g. GRS mines groestl but its block id is single-round `"sha256"` |
| `poolAddress` | pool payout address | `type`: `p2pkh`/`p2sh`/`p2wsh`/`pubkey`/`scripthash` |
| `rewardRecipients` | fee addresses + shares | `percent: 0.01` = 1% |
| `daemons[]` | node RPC | supports redundancy |
| `p2p` | direct node link for fast block notifications | may be `null` → falls back to `blockRefreshInterval` polling |
| `blockRefreshInterval` | new-block poll interval (ms) | 1000 recommended when `p2p` is null |
| `ports` | Stratum ports + vardiff | key is the port number |
| `storage` | Redis | shares / stats / payments |
| `engine` | pluggable engine name | omit (or `"gbt"`) for GBT coins |

Registered algorithms: `sha256`, `sha256d`, `scrypt`, `x11`, `keccak`, `groestl`,
`lyra2rev2`, `verthash` (plus `neoscrypt` with `-tags neoscrypt`). Configuring an
unregistered algorithm fails at startup and lists the supported ones.

> **verthash:** first use generates a ~1.2GB `verthash.dat` under `~/.powcache`
> (slow) and keeps it resident. The pool warms it at startup via `algorithm.Warmup`
> so the first share doesn't stall.

## Add a header-hash algorithm (A → B)

The algorithm system is a pluggable registry (`algorithm/algorithm.go`).

### Pure Go (preferred)

Register in an `init()` — no core changes:

```go
// algorithm/register_myalgo.go
package algorithm

func init() {
    // name (matched lower-case), default multiplier, hash function
    RegisterHash("myalgo", 16, func(header []byte) []byte {
        out := make([]byte, 32)
        // ... hash header into out (big-endian, same byte order as sha256d)
        return out
    })
}
```

Then set `"algorithm": { "name": "myalgo", "sha256dBlockHasher": false }`.

### cgo binding (groestl/lyra2rev2/neoscrypt/verthash …)

Most GPU algorithms only have C implementations. Put the C source under
`algorithm/<algo>/`, write a cgo wrapper exposing `Hash([]byte) []byte`, register it
from an `init()` behind a build tag, and build with `CGO_ENABLED=1 go build -tags
<algo> ./cmd/nomp`. `neoscrypt` (`github.com/sparkspay/go-neoscrypt`) is a worked example.

## Payments (configurable reward scheme, fork-configurable)

Set `disablePayment: false` and add a `payment` block to pay miners automatically.
When a share solves a block the current round is sealed; once the block's coinbase
reaches maturity its reward is attributed to miners per the selected `payMode` and
paid via `sendmany`. Balances below `minPayment` carry over.

```json
"disablePayment": false,
"payment": {
  "interval": 600,           // seconds between payout runs
  "minPayment": 0.05,        // min coin owed before a miner is paid (else carried over)
  "daemon": 0,               // index into daemons[] used for wallet RPC
  "payMode": "prop",         // "prop" | "pplns" | "solo"
  "pplnsWindow": 0,          // pplns look-back as total share difficulty; 0 = the block's round
  "magnitude": 0,            // base units per coin (1e8); 0 = auto-detect
  "minConfirmations": 100,   // coinbase maturity before a reward is paid
  "addressCheckMethod": "getaddressinfo", // or "validateaddress" on older forks
  "sendManyDummy": "",       // leading sendmany arg (Bitcoin Core wants "")
  "omitSendManyDummy": false // true if the fork's sendmany drops the leading arg
}
```

**`payMode`** selects the reward scheme:
- **`prop`** (default) — split the block reward proportionally to the shares of
  the round that found it.
- **`pplns`** — split proportionally to the last `pplnsWindow` difficulty of
  shares across rounds (a sliding window; resists pool-hopping). `pplnsWindow: 0`
  falls back to the block's own round.
- **`solo`** — the miner who found the block takes the whole reward.

The remaining knobs let one binary pay out across bitcoind-family **forks** whose
wallet RPC differs: coinbase maturity (`minConfirmations`), coin precision
(`magnitude`), the address-ownership check (`addressCheckMethod`), and the
`sendmany` shape (`sendManyDummy` / `omitSendManyDummy`). The payout wallet must
own `poolAddress` (verified at startup). Miners are paid to their worker name, so
they connect with their wallet address as the username.

## Add a category-C coin (an engine)

Category-C coins are already supported by pluggable engines — you don't write one,
you enable it: set `"engine": "<name>"` and build with the matching tag if any.

| Coin | `engine` | Build |
|------|----------|-------|
| Ravencoin | `kawpow` | default |
| Zcash / Flux | `equihash` / `zelhash` | default |
| Ergo | `ergo` | default |
| Beam | `beam` | default |
| Alephium | `alephium` | default |
| Ethereum Classic | `ethash` | `-tags ethash` |
| Monero | `cryptonote` | `-tags randomx` |
| Kaspa | `kaspa` | `-tags kaspa` |

Per-engine example configs live under `engine/<name>/`. Architecture and how to add
a brand-new engine: [docs/PLUGGABLE_ENGINES.md](docs/PLUGGABLE_ENGINES.md). Every
engine is end-to-end validated on a real node in CI — [docs/E2E.md](docs/E2E.md).
