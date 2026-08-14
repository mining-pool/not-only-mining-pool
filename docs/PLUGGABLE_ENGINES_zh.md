# 可插拔挖矿引擎（支持 C 类币的架构方案）

> English: [PLUGGABLE_ENGINES.md](PLUGGABLE_ENGINES.md)

本文回答：**C 类币（ETC/XMR/ZEC/RVN/KAS/ERG…）能否通过"可插拔解析器"接入，复用 node 交互以外的所有组件？**

## 1. 先纠正一个前提

对 **A/B 类**（sha256d/scrypt/x11/keccak/groestl…）而言，"复用一切、只换 node 交互"**已经成立**：

- node 交互本身就是配置项（`daemons` 的 host/port/user/pass）；
- 币间唯一算法差异由 `algorithm.RegisterHash` 插件化解决。

换句话说，A/B 类根本不需要新架构，改配置（+ B 类加一个哈希绑定）即可。

但 **C 类的差异不在 node 交互**，而在**挖矿模型本身**。只加一个"node 响应解析器"是不够的——
因为区块头结构、PoW 校验、出块组装、以及 **Stratum 方言**（subscribe/notify/submit 的报文格式）都变了。
真正需要的是一个**可插拔的 Job 引擎**。

## 2. 复用边界（谁共享、谁按引擎实现）

```
┌─────────────────────────── 共享底座（所有引擎复用，零改动）───────────────────────────┐
│  stratum TCP server · client 生命周期 · vardiff · banning · redis 存储 · payments · API   │
└───────────────────────────────────────────────────────────────────────────────────────┘
            ▲  Session 接口（ExtraNonce1 / Difficulty / WorkerName / Send …）
            │
┌───────────┴──────────────── engine.Engine（每币实现）────────────────────────────────┐
│  Init(node)      —— 连接节点、拉首个 work                                                │
│  Poll(newWork)   —— 监听新块/新模板，通知底座重广播                                       │
│  OnSubscribe     —— mining.subscribe 方言（extranonce 分配）                             │
│  JobNotification —— notify 报文（方法名 + 参数，方言相关）                                │
│  OnSubmit        —— 校验提交、PoW 验证、达网络难度则出块提交节点                          │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

接口定义见 [`engine/engine.go`](../engine/engine.go)（已随仓库提供，编译通过、未接入现有通路，零风险）。

### 现有 Bitcoin 通路 = 参考实现

现在的 `daemons/` + `jobs/` + `stratum.Client` 的 bitcoin 分支，就是一个隐式的 GBT 引擎。
把它抽取成满足 `engine.Engine` 的 `gbt.Engine` 是**纯重构**（行为不变），也是接任何新引擎的第 0 步。
接缝已经很清晰：

| Engine 方法 | 现有对应代码 |
|-------------|--------------|
| `OnSubscribe` | `stratum.Client.HandleSubscribe`（extranonce 回包）|
| `JobNotification` | `jobs.Job.GetJobParams` + `SendMiningJob` 里的 `"mining.notify"` |
| `OnSubmit` | `stratum.Client.HandleSubmit` → `jobs.JobManager.ProcessShare` |
| `Poll` | `pool` 里的 `blockPollingIntervalTicker` + p2p 新块事件 |
| `Init` | `daemons.DaemonManager` + `jobs.NewJobManager` |

## 3. 逐币可行性与工作量

| 币 | 算法 | node 接口 | 复用程度 | 需新写 | 工作量 | 结论 |
|----|------|-----------|----------|--------|--------|------|
| **RVN** | KawPow | getblocktemplate ✅ | **高**（coinbase/merkle/GBT 全复用）| PoW 签名扩展 + kawpow 方言 + 出块塞 nonce/mixhash | 中 | **首选** |
| **ETC** | Etchash | eth_getWork/eth_submitWork | 中（节点建块，矿池只做 nonce 中介）| ethproxy/stratum 方言 + etchash 校验 + DAG | 中 | **推荐第二** |
| **ZEC** | Equihash | getblocktemplate ✅ | 中（coinbase/merkle 复用）| 新头字段 + blake2b + solution + zec 方言 | 中高 | 可行 |
| **XMR** | RandomX | getblocktemplate(cn) | 低（CryptoNote，仅底座复用）| blob+reserved 模型 + RandomX VM(cgo) + cn 方言 | 高 | 可行但重 |
| **ERG** | Autolykos2 | 自定义 | 低 | 自定义头 + 查表 PoW + 方言 | 高 | 小众，不建议 |
| **KAS** | kHeavyHash | gRPC(DAG) | **极低**（blockDAG 非线性链）| 几乎全部 | 很高 | **不建议** |

### 3.1 KawPow(RVN) 落地要点（首选）

- **复用**：`getblocktemplate`、coinbase 构造、merkle 计算、80 字节头组装几乎照搬。
- **PoW 签名要扩展**：当前 `algorithm.HashFunc = func([]byte) []byte` 无法表达 kawpow，
  它需要 `(headerHash [32]byte, height uint64, nonce uint64) -> (mixHash, finalHash [32]byte)`。
  在 `engine` 内部实现即可，不必污染 A/B 的哈希注册表。
- **方言**：`mining.notify` 参数是 `[jobId, headerHash, seedHash, target, cleanJobs, height, bits]`；
  矿机提交 `[worker, jobId, nonce, headerHash, mixHash]`；校验用 kawpow 复算 mixHash 并比对 target。
- **出块**：把 nonce(64bit) 与 mixHash 写回区块头后 `submitblock`。
- **依赖**：需要 kawpow 的实现（cgo 绑定 libethash/kawpow，或纯 Go 移植）。

### 3.2 Ethash/Etchash(ETC) 落地要点

- **模型不同**：矿池**不建块**。`eth_getWork` 返回 `[headerHash, seedHash, target]`，
  矿机返回 `nonce + mixHash`，矿池 `eth_submitWork` 交回节点，由节点组装广播。
- **复用**：stratum server / vardiff / banning / storage / API 全复用；`Poll` 改成轮询 `eth_getWork`。
- **不复用**：coinbase/merkle/头序列化统统不用（`OnSubmit` 只做 etchash 难度校验 + `eth_submitWork`）。
- **方言**：常见 ethproxy 方言（`eth_submitLogin`/`eth_getWork`/`eth_submitWork`）或 nicehash ethash stratum。
- **依赖**：etchash（含 DAG 生成，cgo 绑定或纯 Go 实现）。

## 4. 建议的落地顺序

1. **第 0 步（纯重构）**：把现有 bitcoin 通路抽成 `gbt.Engine`，`pool` 通过 `engine.Get(cfg.Engine)` 装配；
   `config` 增加 `"engine": "gbt"`（缺省 gbt，向后兼容）。此步不改任何行为，可用现有 regtest e2e 回归。
2. **第 1 步**：实现 `kawpow.Engine`（复用最多、验证 kawpow 算法绑定）。
3. **第 2 步**：实现 `ethash.Engine`（验证"节点建块型"引擎，打通非 GBT 路径）。
4. 之后按需 Equihash / RandomX。

> 每一步都能在 **regtest** 上端到端验证：RVN 有 regtest、ETC 可用 `--dev` 私链、ZEC 有 regtest。
> 唯有 KAS 这类 blockDAG 无法纳入本架构，明确不做。

## 5. 结论

- **A/B 类**：无需新架构，配置(+哈希绑定)即可——"复用一切、只换 node"本就成立。
- **C 类**：把差异抽象为可插拔 `engine.Engine`（骨架已就绪）后，**RVN(KawPow) 与 ETC(Ethash) 可以接入**，
  复用 stratum/vardiff/banning/storage/payments/API 全套底座；ZEC/XMR 可行但更重；**KAS 不建议**。
- 关键澄清：C 类的可插拔点是**挖矿模型（job 引擎 + stratum 方言）**，不是"node 响应解析器"。

---

## 6. Ethash/Etchash(ETC) 引擎实现进度

代码：[`engine/ethash/`](../engine/ethash/)。示例配置：[`engine/ethash/config.ethereumclassic.example.json`](../engine/ethash/config.ethereumclassic.example.json)。

### 6.1 已实现且已单测（`go test ./engine/ethash/`）

- **node 交互**：`rpc.go` —— 极简 eth JSON-RPC 2.0 客户端，`eth_getWork` / `eth_submitWork` / `eth_blockNumber`（用 httptest 假节点测过）。
- **work 生命周期**：`RefreshWork()` 拉取并解析 `[headerHash, seedHash, target, blockNumber]`，
  header 变化才通知重广播；`CurrentWork()` / `JobParamsForDifficulty(diff)` 生成每连接的 ethproxy work 包（含按矿机难度算的 share target）。
- **难度↔target 数学**：`math.go` —— 2^256 空间的 `TargetFromDifficulty` / `DifficultyFromResult` / `MeetsTarget`（往返测过）。
- **PoW 校验**：`OnSubmit` 用 `go-etchash`(light cache, 支持 ECIP-1099) 复算 `mixDigest/result`，
  校验 mixHash 反作弊、share/网络 target，达网络难度则 `eth_submitWork` 出块。
- **可插拔注册**：`init()` 注册 `"ethash"`/`"etchash"` 到 `engine.Register`。

### 6.2 已完成：stratum 路由集成（增量、零侵入 bitcoin 通路）

引擎已接入整条链路，bitcoin 通路在 `Engine == nil` 时**逐字节不变**：

1. **stratum.Client** 增加 `Engine` 字段；`HandleMessage` 顶部分流到 `handleEngineMessage`
   （`stratum/engine.go`）——`eth_submitLogin`→`OnSubscribe`+推 work、`eth_getWork`→
   `JobParamsForDifficulty(client diff)`、`eth_submitWork`→`OnSubmit`（未授权先拒）、
   `eth_submitHashrate`→`true`。ethproxy 的"推送"= `id:0` 的 result 回包（`Session.Send` method 传空即可）。
2. **engine.Session 适配器**：`engineSession{sc}` 薄封装已有字段，编译期断言满足接口；
   vardiff 复用：valid share 触发 `applyEngineVarDiff`，新难度直接换 work 里的 target 并重推。
3. **Server/Pool 装配**：`Server.Engine != nil` 时跳过 bitcoin rebroadcast，改跑 `Engine.Poll(newWork)`
   → `BroadcastEngineWork()`（每连接按各自难度出 target）。`pool.NewEnginePool` 只装配共享底座
   （stratum+storage+banning+API），`main` 按 `"engine"` 字段自动分发。
4. **构建**：`go build -tags ethash` 才编入 ETC 引擎（`engines_ethash.go`），默认二进制不含 geth 依赖，
   保持精简（~11.5MB vs ~12.2MB）。选了引擎但没带 tag 会**报错并提示**，绝不静默跑成 bitcoin。

路由层已用假引擎单测覆盖（`stratum/engine_test.go`）：login 推 work（含端口默认难度）、getWork、
valid/invalid submit 回包、未授权拒绝。

**share 落库**：引擎模式的有效/无效 share 现已写入 redis（`stratum/engine.go`
submit 处理里的 `go sc.DB.PutShare(...)`），复用 GBT 的 `PutShare`——统计（算力、
有效/无效计数）、按 `miner.rig` 拆分、轮次贡献与 PPLNS 日志均可用，且按**分配难度**
计量（非达成难度）。出块/派奖仍取决于各币种钱包模型：引擎 share 不带 bitcoin 式
`BlockHex`/coinbase txid，故经 bitcoin-family payer 自动派奖需按币种单独接入。

> 后续仍建议做"Step 0 重构"（抽 `gbt.Engine`），让两条通路都走 `engine.Engine`，彻底解耦。

### 6.3 实机联调（ETC 私链）

```bash
# 构建（带 ethash 引擎）
go build -tags ethash ./cmd/nomp
# 配置：engine/ethash/config.ethereumclassic.example.json 拷为 config.json 并改节点/redis
./not-only-mining-pool -c config.json
```

- 用 **core-geth** 起 ethash 私链（PoW，非 clique）：自定义 genesis，`--mine=false`，开放
  `--http --http.api eth,web3 --http.port 8545`。私链若不激活 ECIP-1099，把
  `ETC_ECIP1099_FBLOCK` 设为极大值或按你的 genesis 设定。
- redis 与 bitcoin 通路一样是启动硬依赖（storage 构造时 Ping）。
- 矿机用支持 etchash/ethproxy 的（如 `ethminer`/`lolMiner`）连 `stratum port`。
- 判定：矿池日志出现 `ethash block sealed at height ...`，且 `eth_blockNumber` 增长。

> 注意：`ecip1099FBlock` / epoch 长度必须与你的链一致，否则 `Compute` 出的 mix 对不上、share 全被拒——
> 这是接 etchash 最常见的坑。

---

## 7. KawPow(RVN) 引擎实现进度

代码：[`engine/kawpow/`](../engine/kawpow/)。示例配置：[`engine/kawpow/config.ravencoin.example.json`](../engine/kawpow/config.ravencoin.example.json)。
**纯 Go（powkit），无 cgo、无 build tag，默认编入二进制。**

### 7.1 已实现且已测试

- **GBT 复用**：直接用 `daemons.DaemonManager` + `jobs.NewJob`——coinbase、merkle、target、txdata
  全部复用 bitcoin 机制；KawPow 矿机滚 64 位头 nonce 而非 coinbase，故 coinbase extranonce
  按 job 固定（job 计数器保证唯一）。
- **字节序约定**（对照 Ravencoin `src/primitives/block.cpp` 与 `src/hash.cpp` 源码逐条核实）：
  - kawpow 输入 = `reverse(sha256d(80B: version|prev|merkle|time|bits|height))`（对应 `to_hash256(GetHex())`）
  - 数值判定 = digest 按**大端**解释，`<=` GBT target 即出块
  - 120 字节完整头 = 80B ‖ nonce64(LE) ‖ `reverse(mix)`（对应 `uint256S(to_hex(mix))` 的内序）
- **PoW 校验**：powkit 轻缓存（~16MB/epoch，epoch=7500 块），Init 时异步预热；
  **已用 powkit 的真实 RVN 向量跑通完整 `OnSubmit` 路径**（mix 反作弊、去重、低难度拒绝均有断言）。
- **Stratum 方言**（kawpowminer/t-rex 风格）：`mining.subscribe` → `[null, extraNonce1]`（矿机 nonce
  高位必须以之为前缀，提交时校验）；`mining.authorize` → true + 推 `mining.notify`
  `[jobId, headerHash, seedHash, shareTarget, clean, height, bits]`（share target 内嵌，无 set_difficulty）；
  `mining.submit` → `[worker, jobId, 0xnonce16, 0xheaderHash, 0xmixHash]`。
  路由层为此扩展了 `NotifyMethod()` 可选接口（ethproxy 引擎仍走 id:0 裸 result），并有方言单测。
- **seedHash**：epoch 迭代 keccak256（epoch-1 已用公开常数 `290dec...` 锁定）。

### 7.2 待实机验证 / 已知限制

- 需要在 **ravend regtest**（`ravend -regtest`，kawpow 在 regtest 从 0 激活）+ kawpowminer/t-rex
  实测：`getblocktemplate` 字段兼容性、`submitblock` 接受、真实矿机握手细节。
- share 入库同 ethash 引擎的 TODO（不影响出块，影响统计/支付）。
- 顺带修复了 `jobs.NewJob` 的预置 bug：GBT 无 `target` 字段时 `BigIntFromBitsHex`
  的返回值被丢弃（RVN/部分币只给 bits）。

---

## 8. Equihash(ZEC/Flux) 引擎实现进度

代码：[`engine/equihash/`](../engine/equihash/)。示例配置：`config.zcash.example.json` / `config.flux.example.json`。
**纯 Go（powkit blake2b 校验，无 cache/DAG/cgo），默认编入二进制。**

### 8.1 已实现且已测试

- **变体参数化**：`"engine":"equihash"`（ZEC 200,9 "ZcashPoW"）/ `"zelhash"`（Flux 125,4）；
  solution 最小编码长度按 (n,k) 推导（ZEC=1344B，Flux=52B），兼容带/不带 compact-size 前缀的提交。
- **零 coinbase 构造**：zcashd/fluxd 的 GBT 直接给完整 `coinbasetxn`（含 funding streams），
  矿池不拼 coinbase；merkle root 优先取 GBT `defaultroots.merkleroot`，否则本地按 bitcoin 规则计算。
  `hashBlockCommitments` 依次取 `defaultroots.blockcommitmentshash` → `finalsaplingroothash` → 零。
  引擎自带 GBT 调用（不带 segwit rules——zcashd 不认）。
- **140 字节 zcash 头**：version(LE)|prev|merkle|reserved|time(LE)|bits(LE)|nonce32，
  nonce = nonce1(4B, 池分配) ‖ nonce2(28B, 矿机滚)；区块哈希 = sha256d(头‖varint(solLen)‖solution)。
- **str4d zcash stratum 方言**：subscribe → `[null, nonce1]`；每次推送 =
  `mining.set_target` + `mining.notify [jobId, verLE, prev, merkle, reserved, ntimeLE, bitsLE, clean]`。
  路由层为此新增 `TargetParams()` 可选能力（set_target 先行推送，有方言单测）。
- **完整 OnSubmit 已用 powkit 的真实 Flux 区块向量锁定**（140B 真头 + 52B 真解）：
  有效 share、带前缀 solution、坏 ntime / 坏 solution / 未知 job / 重复提交全部按预期。

### 8.2 待实机验证 / 已知限制

- 需要 **zcashd regtest**（Equihash 参数在 regtest 是 48,5！须在配置或代码上按链参数切换——
  这是实测时的头号坑）或 fluxd 实链联调；矿机用 gminer/lolminer/miniZ。
- ntime 只接受与 job 相等（不支持 time rolling，主流 equihash 矿机不滚时间）。
- share 入库 TODO 与其他引擎相同。

---

## 9. CryptoNote(XMR/RandomX) 引擎实现进度

代码：[`engine/cryptonote/`](../engine/cryptonote/)。示例配置：`config.monero.example.json`。
**构建**：`CGO_ENABLED=1 go build -tags randomx ./cmd/nomp`（RandomX 静态库已按平台预编译进 `github.com/mining-pool/go-randomx` 模块，直接链接）。
默认构建会注册引擎但 `Init` 明确报错提示加 tag——绝不静默放行不可验证的 share。

### 9.1 关键澄清（回应"XMR 差的也不多"）

正确。之前把 XMR 与 KAS 同归"不兼容"是**归类错误**：XMR 是单链 + JSON-RPC + 节点全建块
（`get_block_template` 连 coinbase 都替你写好，只留 `reserved_offset` 给池），引擎模型完全放得下。
真正的硬骨头只有两块，本轮都已解决：

1. **RandomX 无纯 Go 实现 → 升级了 `mining-pool/go-randomx` 绑定**（见 `github.com/mining-pool/go-randomx`，
   可直接推回上游）：RandomX **v1.2.1**（含 Apple Silicon JIT）、新增 **light 模式 VM**
   （矿池校验只需 ~256MB cache，无需 2GB dataset；旧绑定 nil dataset 直接 panic，已修）、
   per-OS 链接 flags（macOS 不支持 `-static`，已拆分）、补 `go.mod` 与 `GetFlags()`。
   **官方 RandomX 测试向量通过**（"test key 000"/"This is a test" → `639183aa...`）。
2. **塞 extranonce 后要重建 hashing blob** → 纯 Go 实现 varint 解析、区块 blob 走读
   （定位 nonce 偏移与 miner tx 区间，支持 v15 tagged-key 输出）、**三段式 miner tx hash**、
   **Monero tree_hash 忠实移植**——用 monero-project 官方 C 源码（tree-hash.c + keccak.c）
   本地编译生成 16 组参照向量，Go 移植 **16/16 一致**（tree_hash 有个反直觉的
   "叶层部分压缩"步骤，凭记忆写必错，所以必须对拍）。

### 9.2 方言与路由

XMRig 方言的两个特殊点已在路由层泛化支持（各有单测）：
- **object 参数**：`login`/`submit` 的 params 是 JSON 对象不是数组。为此把
  `JsonRpcRequest.Params` 改为 `json.RawMessage`（数组用 `ParamsArray()` 访问，bitcoin 通路不变），
  顺带修复了两个预置问题：object params 会直接杀连接；`HandleSubmit` 越界索引 panic。
- **login 一步到位**：subscribe+authorize+首个 job 合一，reply 内嵌 job；推送用
  `{"method":"job","params":{...}}`。新增 `ObjectParams()` 引擎能力；`getjob`/`keepalived` 已路由。

### 9.3 已测试

- blob 层（默认构建就跑）：tree_hash 16 向量、varint 往返、合成区块 blob 解析/重建/
  extranonce 敏感性、v2 miner tx 三段哈希结构。
- `-tags randomx`：**真实 RandomX light VM 驱动完整 `OnSubmit`**——诚实 share 接受、
  重复 nonce 拒绝、伪造 result 拒绝、未知 job 拒绝；XMRig compact target 编码往返。
- 绑定本身：官方 RandomX 向量 + 换 seed 重键。

### 9.4 待实机验证 / 已知限制

- 需要 monerod（`--regtest --offline` + `generateblocks` 可秒出块）+ XMRig 实测。
- **当前所有矿机共享 nonce 空间**（4 字节头 nonce，XMRig 默认随机起点）：碰撞只浪费 share
  不会出错块；后续可把 per-client extranonce 写进 reserved 区（需给 JobParamsForDifficulty
  传 Session 身份，接口小改）。
- seed 轮换（每 2048 块）时 light cache 重建约 1 秒，期间提交串行等待——矿池规模大时
  可预热 next_seed_hash（模板已带，未做）。
- share 入库 TODO 与其他引擎相同。

---

## 10. Kaspa(kHeavyHash/blockDAG) 引擎 —— 本轮改动最大

代码：[`engine/kaspa/`](../engine/kaspa/)。构建：`CGO_ENABLED=1 go build -tags kaspa ./cmd/nomp`。

这是回答"KAS 为什么难"的落地：难点**不是哈希**（powkit 有 kHeavyHash），而是三处，全部用**可插拔方式**解决，没动其它引擎：

1. **流式工作源**：Kaspa **1~10 块/秒**，轮询无效。`engine.Engine.Poll(chan)` 契约本就抽象了工作源——
   本引擎在 `Poll` 内用 kaspad 的 gRPC 通知流（`RegisterForNewBlockTemplateNotifications`）驱动，
   带 2s 兜底 ticker；轮询式引擎（ERG/ETC…）仍用自己的 ticker。**接口零改动**——这正是"流式"这个
   最大改动点最终没有外溢成框架改动的原因。
2. **官方共识代码**：节点是 gRPC/protobuf，区块头哈希是对 typed 结构做 blake2b。**不自造共识**——
   直接依赖 `github.com/kaspanet/kaspad`（rpcclient + domain/consensus/pow）。依赖重，故 `-tags kaspa` 隔离。
3. **依赖冲突修复**：kaspad 拉入现代 otel，与老 `go-redis v8.3.1`（旧 otel `/api/*` 路径）不可共存——
   升级 `go-redis → v8.11.5`（API 兼容）+ 钉 otel v1.31.0 解决。默认构建不受影响。

**PoW 正确性交叉验证**（`TestHeavyHashCrossImplementation`）：同一 header，kaspad **官方 pow.State** 与
powkit **独立 heavyhash 实现**必须算出同一 PoW 值——两套代码互证，锁死字节序与算法接线。
方言（kaspa-stratum-bridge 惯例，lolMiner/BzMiner 通用）：subscribe→`[true,"EthereumStratum/1.0.0"]`、
set_difficulty 前置、notify 的 largeJob = 4×BE-uint64(prePowHash)‖LE-uint64(timestamp)、submit `[worker,jobId,nonce]`。

**待实机**：kaspad `--utxoindex` + lolMiner；**DAG 记账**（blue score 确认、红块正常）需支付逻辑 DAG 化，出块与 share 不受影响。

## 11. Ergo(Autolykos2) 引擎 —— 本轮最便宜

代码：[`engine/ergo/`](../engine/ergo/)。纯 Go（powkit + REST），默认编入。

节点建块型，最简单：`GET /mining/candidate → {msg, b(target), h}`，池只滚 nonce，`POST /mining/solution`。
PoW 用 powkit autolykos2，**官方 Ergo 向量锁定**（`msg=548c3e60… nonce=0x3105 height=614400 → 0002fcb1…`），
完整 `OnSubmit` 用该向量跑通（含伪造/重复/短 nonce 拒绝）；REST 客户端处理超 int64 的大 target（json.Number）。
方言：subscribe→`[null,nonce1]`、set_target 前置、notify `[jobId,height,msg,"",clean]`、submit `[worker,jobId,nonce]`。
**待实机**：ergo-node + nbminer/lolMiner。

## 12. 剩余两个的现状（BEAM / ALPH）

诚实结论：**都不是不可能，是性价比排序靠后 + 需要新传输层**，PoW 层都已不是障碍。

- **BEAM(BeamHashIII)**：powkit `NewBeam().Verify` 现成。卡点是**节点即 stratum server 的反转模型**——
  矿池要作为 TLS-JSON 长连接**客户端**去连 beam-node 并被动收 job，与现有"矿池是 server、engine 拉模板"
  的方向相反，需要一个持久 TLS-JSON 组件；且 Mimblewimble 无地址，支付走 wallet API。
- **ALPH(blake3)**：blake3 有成熟纯 Go 库。卡点有二：① 节点是**自研二进制协议**（TCP 上自定义 framing），
  要从头写编解码；② **blockflow 16 条链同时出 job**，引擎内部需维护 job 集合并按 (fromGroup,toGroup) 路由
  share——注意这仍是**引擎内部**的事（各引擎本就自持 job history），**不需要改 `engine.Engine` 接口**。

两者都能按现有可插拔引擎模式接入，只是各自要先写一个专用传输层。

---

## 13. 事件优先的可插拔工作源（worksource）

引擎"如何得知新工作"抽象成独立组件 [`engine/worksource`](../engine/worksource/)，事件为一等公民，Poll 仅为兜底。接口从 `Poll(chan)` 改为 `Watch(onNewWork func())`。

组件：
- `Poll(interval, refresh)` —— 轮询源；无 push API 的节点或事件源旁的兜底。
- `Subscribe(name, open, refresh)` —— 事件源；`open` 阻塞消费订阅、每事件触发 `refresh`，断线自动重连。
- `ZMQSource(name, endpoint, topic, refresh)` —— 基于 `github.com/go-zeromq/zmq4`（纯 Go）的 ZMQ SUB 事件源。
- `Run(emit, sources...)` —— 并发竞速多个源（事件源 + 慢速 Poll 兜底）。

各引擎工作源：

| 引擎 | 事件源 | 兜底 |
|------|--------|------|
| KAS | kaspad gRPC 新模板通知（`Subscribe`）| 5s Poll |
| XMR | monerod ZMQ `json-minimal-chain_main`（配置 `daemons[].zmq` 时）| Poll |
| RVN | bitcoind ZMQ `hashblock`（配置 `daemons[].zmq` 时）| Poll |
| ZEC/FLUX | zcashd/fluxd ZMQ `hashblock`（配置 `daemons[].zmq` 时）| Poll |
| ETC | —（geth 推送为 WebSocket，未接）| eth_getWork Poll |
| ERG | —（节点仅 REST，无 push）| Poll |

配置：给对应 `daemons[]` 加 `"zmq": "tcp://127.0.0.1:28332"`（bitcoind 需 `-zmqpubhashblock`，monerod 需 `--zmq-pub`）即启用事件；留空则纯 Poll。事件源与 Poll 兜底并行竞速，事件源失效时 Poll 保证不丢工作。

---

## 14. BEAM 与 ALPH 传输层

两者都是纯 Go（无 cgo），默认编入二进制。

### BEAM（BeamHash III）—— TLS-JSON 客户端

代码：[`engine/beam/`](../engine/beam/)。示例：`config.beam.example.json`。

节点即 stratum server，矿池作为**客户端**连 beam-node 的 stratum 端口（默认 TLS，自签证书），
按行分隔 JSON：`login`(带 api_key) → 节点回 `result`(带 `nonceprefix`) → 节点推 `job`(`input`32B, `difficulty`)
→ 矿池转发 `solution`(`nonce`8B, `output`104B)。工作源由节点推送天然事件驱动（`worksource.Subscribe`），断线重连+重登录。
PoW 用 powkit BeamHash III 校验 40 字节头(input‖nonce)+104 字节解，**官方 Beam 向量在 `OnSubmit` 全程锁定**；
矿池转发所有 equihash 有效解，最终难度/target 由节点裁决。
- 配置：`daemons[0].password` 填 beam-node 的 `--stratum_secret`/api_key；`daemons[0].tls` 非空即用 TLS（自签证书跳过校验）。
- 待实机：beam-node `--stratum_port` + 支持 beam stratum 的矿机。

### ALPH（Alephium / 双 Blake3）—— 二进制协议 + 多链

代码：[`engine/alephium/`](../engine/alephium/)。示例：`config.alephium.example.json`。

节点 miner API（默认 10973）二进制协议：`uint32BE(len)‖version‖type‖payload`；`Jobs(0x00)` 一次推
**每条链一个 job**（blockflow groups×groups），矿池维护整套 job 集合，提交按 jobId 路由回对应链。
帧编解码与 Jobs 解析按官方 `Message.scala` 格式实现并有往返测试。PoW = `blake3(blake3(nonce24‖headerBlob))`，
`BigInt(1,hash) < target`（对照官方 `PoW.scala` 的 `doubleHash` + `checkWork`），blake3 用 `lukechampine.com/blake3`；
出块 blockBlob = `nonce24‖headerBlob‖txsBlob` 经 `SubmitBlock(0x00)` 回传。
- 多链：`JobParamsForDifficulty` 一次返回全部链 job 的数组，`OnSubmit` 按 jobId 定位链。
- 待实机：alephium-node miner API + 支持 ALPH 的矿机；矿机侧 notify 字段顺序需与目标矿机对齐。
