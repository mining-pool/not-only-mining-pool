# 主流矿币适配教程（Top-20）

本教程说明如何用本矿池（NOMP / go-stratum-pool）接入主流 PoW 矿币，并解释**哪些能接、哪些不能接、以及为什么**。

---

## 0. 一分钟看懂本矿池的适配边界

本矿池是一台 **Bitcoin `getblocktemplate`（GBT）风格的 Stratum 服务器**：

- 它向币种全节点请求 `getblocktemplate`，自己拼装 coinbase、算 merkle root，
  组装出**标准的 80 字节比特币区块头**（见 `jobs/job.go` 的 `SerializeHeader`）；
- 校验 share 时，它对这 80 字节区块头做一次 **PoW 哈希**，比较难度；
- 出块时通过 `submitblock` / p2p 广播。

**关键结论：不同币种之间唯一变化的，就是"对区块头做哈希"的那个函数。**

因此适配矿币分成三类：

| 类别 | 说明 | 接入成本 |
|------|------|----------|
| **A. 头哈希兼容** | PoW = f(80 字节头)，如 sha256d / scrypt / x11 / keccak / groestl / neoscrypt / lyra2 / x16r… | 只需提供 `f`，其余零改动 |
| **B. 头哈希兼容但需绑定** | 同上，但 Go 无纯实现，需要 cgo 绑定 C 库 | 加一个 `RegisterHash` 绑定文件 |
| **C. 引擎不兼容** | 区块头结构 / 挖矿模型完全不同：Ethash、RandomX、Equihash、KawPow、kHeavyHash… | 需要另写 job 引擎，不在本矿池范围 |

---

## 1. Top-20 主流矿币归类表

> 排名综合考虑市值与算力规模，PoS 币（ETH、ADA、SOL…）不可挖，不在此列。

### A 类：开箱即用（本仓库已原生支持）

| # | 币 | 符号 | 算法 | 模板 | 状态 |
|---|-----|------|------|------|------|
| 1 | Bitcoin | BTC | sha256d | `coins/config.bitcoin.json` | ✅ 原生 |
| 2 | Bitcoin Cash | BCH | sha256d | `coins/config.bitcoincash.json` | ✅ 原生 |
| 3 | Bitcoin SV | BSV | sha256d | `coins/config.bitcoinsv.json` | ✅ 原生 |
| 4 | Litecoin | LTC | scrypt | `coins/config.litecoin.json` | ✅ 原生 |
| 5 | Dogecoin | DOGE | scrypt | `coins/config.dogecoin.json` | ✅ 原生 |
| 6 | Dash | DASH | x11 | `coins/config.dash.json` | ✅ 原生 |
| 7 | DigiByte | DGB | sha256d(多算法) | `coins/config.digibyte.json` | ✅ 原生 |
| 8 | Namecoin | NMC | sha256d | `coins/config.namecoin.json` | ✅ 原生 |
| 9 | Peercoin | PPC | sha256d(PoS 混合) | `coins/config.peercoin.json` | ✅ 原生 |
| 10 | Maxcoin 等 keccak 系 | — | keccak | `coins/config.maxcoin.json` | ✅ 原生（本次新增）|
| 11 | Groestlcoin | GRS | groestl | `coins/config.groestlcoin.json` | ✅ 原生（纯 Go，注意 `blockHasher:"sha256"`）|
| 12 | Vertcoin | VTC | verthash | `coins/config.vertcoin.json` | ✅ 原生（纯 Go；首次自动生成 ~1.2GB verthash.dat）|
| 13 | Monacoin | MONA | lyra2rev2 | `coins/config.monacoin.json` | ✅ 原生（纯 Go）|

另附赠同算法的 A 类模板：eCash(XEC)、Syscoin(SYS)（sha256d）、Viacoin(VIA)、Einsteinium(EMC2)（scrypt）。
注意 BCH/XEC 的 cashaddr 地址格式：配置里请使用 **legacy base58** 地址（节点仍接受）。

### B 类：模板已给，算法用 cgo build tag 接入

| # | 币 | 符号 | 算法 | 模板 | 说明 |
|---|-----|------|------|------|------|
| 14 | Feathercoin | FTC | neoscrypt | `coins/config.feathercoin.json` | ✅ `CGO_ENABLED=1 go build -tags neoscrypt`（`github.com/sparkspay/go-neoscrypt`，profile 0 = FTC 参数）|

> DigiByte 除 sha256d 外还有 scrypt/skein/qubit/odo 多算法端口，把 `algorithm.name` 改成对应算法即可（skein/qubit/odo 属 B 类）。

### C 类：挖矿引擎不兼容（本矿池架构不支持，需另立项目）

| 币 | 符号 | 算法 | 状态 |
|-----|------|------|------|
| Ravencoin | RVN | kawpow | ✅ **已通过可插拔引擎支持**（默认编入，`"engine":"kawpow"`，见 `docs/PLUGGABLE_ENGINES_zh.md` §7）|
| Ethereum Classic | ETC | etchash | ✅ **已通过可插拔引擎支持**（`go build -tags ethash`，`"engine":"ethash"`，见 §6）|
| Zcash / Flux | ZEC/FLUX | equihash/zelhash | ✅ **已通过可插拔引擎支持**（默认编入，`"engine":"equihash"`/`"zelhash"`，见 §8）|
| Monero | XMR | randomx | ✅ **已通过可插拔引擎支持**（`CGO_ENABLED=1 go build -tags randomx`，`"engine":"cryptonote"`，见 §9）|
| Kaspa | KAS | kHeavyHash | ✅ **已通过可插拔引擎支持**（`-tags kaspa`，`"engine":"kaspa"`，用官方 kaspad gRPC+共识，见 §10）|
| Ergo | ERG | autolykos2 | ✅ **已通过可插拔引擎支持**（默认编入，`"engine":"ergo"`，REST + powkit，见 §11）|
| Beam | BEAM | beamhashIII | ✅ **已通过可插拔引擎支持**（默认编入，`"engine":"beam"`，TLS-JSON 客户端，见 §14）|
| Alephium | ALPH | blake3 | ✅ **已通过可插拔引擎支持**（默认编入，`"engine":"alephium"`，二进制协议 + 多链，见 §14）|

**如果你的目标是 C 类币，请不要基于本矿池改——工作量等同重写，用对应生态的专用矿池软件。**

---

## 2. 快速上手（A 类币，5 步）

以 **Litecoin** 为例：

```bash
# 1) 拷贝模板到根目录并改名
cp coins/config.litecoin.json config.json

# 2) 编辑 config.json
#    - poolAddress.address / rewardRecipients[].address 换成你自己的钱包地址
#    - daemons[0] 的 host/port/user/password 换成你的 litecoind 的 RPC
#    - storage 指向你的 redis

# 3) 保证 litecoind 已开启 RPC，且 litecoin.conf 里有：
#      server=1
#      rpcuser=rpcuser
#      rpcpassword=rpcpassword
#      rpcallowip=127.0.0.1

# 4) 编译
go build .

# 5) 启动
./not-only-mining-pool -c config.json -l info
```

矿机连接：`stratum+tcp://<你的服务器IP>:3032`，用户名填钱包地址。

---

## 3. 配置字段速查

| 字段 | 含义 | 备注 |
|------|------|------|
| `coin.name` / `coin.symbol` | 币名/符号 | 仅展示与存储 key |
| `algorithm.name` | 算法名 | 必须在已注册算法内，见下 |
| `algorithm.multiplier` | 难度倍率（2^n 缩放 diff-1）| **可留 0**，会按算法默认值自动填充 |
| `algorithm.sha256dBlockHasher` | 出块时区块 hash 是否用 sha256d | scrypt/sha256d 系填 `true`；x11/keccak 等填 `false`（用算法本身）|
| `algorithm.blockHasher` | 显式指定区块 ID 算法（覆盖上一项）| 特例用，如 GRS 挖 groestl 但区块 ID 是**单轮** `"sha256"` |
| `poolAddress` | 矿池收款地址 | `type`: `p2pkh`/`p2sh`/`p2wsh`/`pubkey`/`scripthash` |
| `rewardRecipients` | 抽水地址与比例 | `percent: 0.01` = 1% |
| `daemons[]` | 全节点 RPC | 支持多节点冗余 |
| `p2p` | 直连节点加速新块通知 | **可为 `null`**，此时靠 `blockRefreshInterval` 轮询 |
| `blockRefreshInterval` | 轮询新块间隔(ms) | p2p 为 null 时建议 1000 |
| `ports` | Stratum 端口与 vardiff | key 是端口号 |
| `storage` | Redis | 存 share/统计/支付 |

**已注册算法**（`algorithm.SupportedAlgorithms()`）：`sha256`、`sha256d`、`scrypt`、`x11`、`keccak`、
`groestl`、`lyra2rev2`、`verthash`。其余算法需按第 4 节自行注册。启动时若填了未注册算法会**直接报错并列出支持列表**。

> **verthash 特别说明**：首次使用会在 `~/.powcache/` 自动生成 ~1.2GB 的 `verthash.dat`（耗时较长）
> 并常驻同等内存。矿池启动时通过 `algorithm.Warmup` 预热，不会卡在第一个 share 上。

### 关于 `multiplier`（难度倍率）

不同算法单次哈希的"重量"差异巨大：scrypt 比裸 sha256d 重约 2^16 倍。`multiplier` 就是把 diff-1 目标做 `2^multiplier` 缩放，让上报算力和 share 节奏在各算法间可比。默认值内置在 `algorithm/algorithm.go` 的 `defaultMultipliers`：sha256d=0、scrypt=16、x11=30、keccak=8。**留空即用默认，无需纠结。**

---

## 4. 新增一个头哈希算法（A→B 类）

算法系统已重构为**可插拔注册表**（`algorithm/algorithm.go`）。你有两种方式加算法。

### 4.1 纯 Go 实现（首选）

若能找到该算法的纯 Go 实现，直接在 `init()` 里注册即可，**不改任何核心代码**：

```go
// algorithm/register_myalgo.go
package algorithm

func init() {
    // 参数：算法名(小写匹配)、默认 multiplier、哈希函数
    RegisterHash("myalgo", 16, func(header []byte) []byte {
        out := make([]byte, 32)
        // ... 对 header 做你的哈希，写入 out（大端，与 sha256d 一致的字节序）
        return out
    })
}
```

之后配置里写 `"algorithm": { "name": "myalgo", "sha256dBlockHasher": false }` 即可。

### 4.2 cgo 绑定 C 库（groestl/lyra2rev2/neoscrypt/verthash 等）

绝大多数 GPU 算法只有 C 实现。以 **neoscrypt** 为例：

1. 把 C 源码（如 `neoscrypt.c/.h`，可从 cpuminer-multi 取）放到 `algorithm/cneoscrypt/`。
2. 写 cgo 包装：

```go
// algorithm/cneoscrypt/neoscrypt.go
package cneoscrypt

/*
#cgo CFLAGS: -O3
#include "neoscrypt.h"
*/
import "C"
import "unsafe"

func Hash(input []byte) []byte {
    out := make([]byte, 32)
    C.neoscrypt(
        (*C.uchar)(unsafe.Pointer(&input[0])),
        (*C.uchar)(unsafe.Pointer(&out[0])),
        0x80000620, // neoscrypt profile flags
    )
    return out
}
```

3. 注册（放在会被编译进主程序的地方，例如新建 `algorithm/register_neoscrypt.go`）：

```go
package algorithm

import "github.com/mining-pool/not-only-mining-pool/algorithm/cneoscrypt"

func init() { RegisterHash("neoscrypt", 16, cneoscrypt.Hash) }
```

4. `CGO_ENABLED=1 go build .` 即可，`config.feathercoin.json` 直接可用。

> **字节序坑**：Stratum 校验里 `headerHash` 会被 `utils.ReverseBytes` 反转后比对 target。你的哈希函数应返回与 sha256d 相同约定的 32 字节（内部小端、按原 C 库输出）。接入新算法后，**务必用该币真实区块头做一次已知答案回归测试**（参考 `algorithm/algorithm_test.go` 里 scrypt 的用例）。

### 4.3 常见 B 类算法 C 库来源

| 算法 | 参考 C 库 |
|------|-----------|
| groestl | cpuminer-multi / groestlcoin 官方 |
| lyra2rev2 / lyra2rev3 | cpuminer-multi |
| neoscrypt | cpuminer-multi |
| x13/x15/x16r/x16rv2/x17 | cpuminer-multi（含 hamsi/fugue/shabal/echo 等）|
| verthash | vertcoin 官方（需 `verthash.dat` 数据文件）|
| skein / qubit | cpuminer-multi（DigiByte 多算法）|

---

## 5. p2p 直连（可选加速）

模板默认 `"p2p": null`，即完全靠 RPC 轮询获取新块，最稳、零额外配置。想让"别的矿池出块"时秒级切换 job，可开启 p2p：

```json
"p2p": {
  "host": "127.0.0.1",
  "port": 9333,
  "magic": "fbc0b6db",
  "disableTransactions": true
}
```

`magic`（网络魔数）与 `port` 必须与目标币一致，取自该币源码 `chainparams.cpp` 的 `pchMessageStart`。**填错会连不上或误判，拿不准就保持 `null`。**

---

## 6. 上线前自检清单

- [ ] `go build .` 通过；B 类币用 `CGO_ENABLED=1`。
- [ ] 全节点已完全同步，`getblocktemplate` 能返回（钱包已解锁/有权限）。
- [ ] 新算法跑过**已知答案回归测试**（真实区块头 → 期望 hash）。
- [ ] `poolAddress`、`rewardRecipients` 为**你自己**的有效地址（启动时会校验，非法直接 panic）。
- [ ] Redis 可连；如需支付，`getbalance` 能返回。
- [ ] 先在**测试网**（`coin.testnet` 会由 RPC 自动识别）跑通再上主网。
- [ ] 用真实矿机或 `cpuminer` 连上打几个 share，确认难度/算力显示正常，并等到一个测试网块验证 `submitblock` 成功。

---

## 7. 本次适配改动了什么

- `algorithm/algorithm.go`：算法系统重构为**注册表**，新增 `RegisterHash` / `IsSupported` /
  `SupportedAlgorithms` / `DefaultMultiplier`；原生新增 `sha256`、`keccak`；保留 scrypt/x11/sha256d。
- `pool/pool.go`：启动时校验算法是否受支持；`multiplier` 留空时按算法默认值自动填充。
- `coins/`：新增 14 个主流矿币配置模板（A 类开箱即用，B 类绑定后可用）。

> 一句话：**A 类币改配置即可上线；B 类币按第 4 节加一个绑定文件；C 类币请另寻专用软件。**

---

## 8. 测试每个矿币

测试分两层，**都已随仓库提供**。

### 8.1 自动化测试（无需节点，`go test` 即可）

```bash
go test ./algorithm/ ./coins/ -v
```

覆盖：

- **算法已知答案测试（KAT）**：`algorithm/coins_test.go`
  - `TestKnownAnswer_SHA256D_BitcoinGenesis`：用**比特币创世区块头**验证 sha256d 出块哈希流程，
    这一条即覆盖整个 sha256d 家族（BTC/BCH/BSV/DGB/NMC/PPC，它们序列化与出块识别方式完全一致）。
  - scrypt / x11 / keccak 各有 KAT（`algorithm_test.go`）。
- **Top-20 覆盖矩阵**：`TestTop20Coverage` 断言 20 个币的分类——A 类算法必须已注册、
  C 类算法必须**未**注册（GBT 矿池确实无法服务）。当前结果：**13 A / 1 B / 6 C**
  （GRS/MONA/VTC 已用纯 Go 库转正；C 类中 ETC 另有可插拔引擎路径，见 `docs/PLUGGABLE_ENGINES_zh.md`）。
- **逐币模板校验**：`coins/coins_test.go` 遍历全部 14 个模板，断言 JSON 合法、能解析成
  `config.Options`、算法已注册或属已知待绑定、端口/难度/地址字段齐全、`sha256dBlockHasher` 与算法自洽。

> 说明：算法正确性是**按算法**而非按币区分的（同算法的币共用同一哈希与序列化），
> 所以按算法做 KAT + 按币做配置校验，就完整覆盖了 A 类每一个币的适配正确性。
> B 类算法在你完成 cgo 绑定后，照抄 scrypt 的 KAT 写一条真实区块头回归测试即可。

### 8.2 实机联调（真实出块，需要节点）

**关于 signet 的实话**：signet 只有 BTC、LTC 等极少数币有；DOGE/DASH/BCH/BSV/NMC/PPC/GRS/… 都**没有 signet**。
所以"逐币真实测试"的正确姿势是 **regtest**——几乎所有 bitcoind 变体都支持，难度=1、可秒出块、
可即时 `getbalance`，最适合验证 `getblocktemplate → stratum → submitblock` 整条链路。BTC/LTC 想用 signet，
把 `NET=signet` 传进脚本即可（signet 出块需等待，不如 regtest 快）。

一键脚本 [`scripts/e2e.sh`](scripts/e2e.sh)：起 redis + 节点(regtest) → 预挖 101 块让 coinbase 成熟 →
用节点真实地址渲染 `config.json` → 编译并启动矿池 → （可选）拉起矿机直到区块高度增长（即 `submitblock` 成功）。

```bash
# 例：Litecoin regtest 全自动出块
DAEMON=litecoind CLI=litecoin-cli TEMPLATE=coins/config.litecoin.json \
MINER="minerd -a scrypt" ./scripts/e2e.sh

# 例：Bitcoin，只把矿池拉起来手动连矿机（不自动挖）
DAEMON=bitcoind CLI=bitcoin-cli TEMPLATE=coins/config.bitcoin.json ./scripts/e2e.sh
```

判定标准：脚本打印 `SUCCESS: block height X -> X+1`，且矿池日志出现 `Found Block`。

**逐币测试矩阵**（把上面的三件套换成对应币即可）：

| 币 | DAEMON / CLI | TEMPLATE | MINER 算法 | 网络 |
|----|--------------|----------|-----------|------|
| BTC | bitcoind / bitcoin-cli | config.bitcoin.json | `-a sha256d` | regtest 或 signet |
| BCH | bitcoind(BCHN) / bitcoin-cli | config.bitcoincash.json | `-a sha256d` | regtest |
| BSV | bitcoind(SV) / bitcoin-cli | config.bitcoinsv.json | `-a sha256d` | regtest |
| LTC | litecoind / litecoin-cli | config.litecoin.json | `-a scrypt` | regtest 或 signet |
| DOGE | dogecoind / dogecoin-cli | config.dogecoin.json | `-a scrypt` | regtest |
| DASH | dashd / dash-cli | config.dash.json | `-a x11` | regtest |
| DGB | digibyted / digibyte-cli | config.digibyte.json | `-a sha256d` | regtest |
| NMC | namecoind / namecoin-cli | config.namecoin.json | `-a sha256d` | regtest |
| PPC | peercoind / peercoin-cli | config.peercoin.json | `-a sha256d` | regtest |
| MAX/keccak | 对应 coind/cli | config.maxcoin.json | `-a keccak` | regtest |
| GRS/VTC/MONA/FTC (B 类) | 对应 coind/cli | 对应模板 | 需先完成第 4 节 cgo 绑定 | regtest |

> C 类币（ETC/XMR/ZEC/RVN/KAS/ERG…）不在此矩阵——本矿池架构不支持，无法联调。
