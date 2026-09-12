# Quantus (QTC)

The default build includes `engine: "quantus"`. The node connection uses the
QUIC protocol `quantus-miner/2`. Miner ports can use native QUIC or LuckyPool's
Quantus Stratum dialect over TCP/TLS. Both share local Poseidon2 verification,
duplicate detection, node submission and Redis accounting.

| 矿工 | 接入方式 | 示例端口 |
|------|----------|----------|
| 官方 `quantus-miner`（`quantus-miner/2`） | QUIC + 证书指纹 | 3333/UDP |
| SRBMiner-Multi 3.6.4+ | QUIC + 证书指纹 | 3333/UDP |
| SRBMiner-Multi 3.6.4+ | LuckyPool Quantus Stratum | 3334/TCP 或 3335/TLS |

SRBMiner 的两种模式和证书参数依据其
[3.6.4 发布说明](https://github.com/doktor83/SRBMiner-Multi/releases/tag/3.6.4)及
[官方参数说明](https://github.com/doktor83/SRBMiner-Multi/blob/4babe15ec4bee705ffa7e3dee3717eb87c642e5a/Parameters)。

## 配置与运行

1. 先运行已同步、开启挖矿的 Quantus 节点，在节点上设置矿池的
   `--rewards-inner-hash` 和 `--miner-listen-port 9833`。
   出块奖励地址由节点决定，矿池配置的 `poolAddress` 不会修改该地址。
2. 复制配置，填写节点地址、节点生成的令牌文件和证书指纹文件，以及 Redis：

   ```sh
   cp engine/quantus/config.quantus.example.json config.quantus.json
   ```

   `quantus.nodeAddress` 是节点的 **UDP/QUIC 挖矿端口**，不是 9944 RPC。
   `authTokenFile` 与 `tlsCertSHA256File` 分别指向节点生成的
   `miner-auth-token` 和 `miner-tls-cert-sha256`，需要在矿池机器上可读。
   矿池到节点的连接应使用本机或私有网络。

3. 为矿工连接矿池的 QUIC 端口生成独立证书，计算证书 DER 的 SHA-256 指纹：

   ```sh
   openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
     -keyout quantus-pool-key.pem -out quantus-pool-cert.pem \
     -days 365 -nodes -subj '/CN=quantus-pool'
   openssl x509 -in quantus-pool-cert.pem -outform DER \
     -out quantus-pool-cert.der
   openssl dgst -sha256 quantus-pool-cert.der
   ```

   配置 `ports.3333.tls` 和 `ports.3335.tls` 为该证书和私钥路径。
   示例同时开启 **3333/UDP（QUIC）、3334/TCP、3335/TCP（TLS）**；
   可以删除不需要的端口，或在不同端口配置不同整数难度。
   `protocol` 省略时保留原有 QUIC 行为；TCP 和 TCP/TLS 均设置为 `stratum`，
   由 `tls` 是否为空决定是否启用 TLS。
   启动：

   ```sh
   go build -o nomp ./cmd/nomp
   ./nomp -c config.quantus.json
   ```

4. 使用支持 `quantus-miner/2` 的官方 `quantus-miner` 连接矿池：

   ```sh
   quantus-miner serve \
     --node-addr POOL_IP:3333 \
     --auth-token 'YOUR_ACCOUNT.rig1' \
     --tls-cert-sha256 POOL_CERT_SHA256
   ```

   此处的 `--auth-token` 用作矿池 worker 名称，格式为 `账户.矿机名`，
   长度为 1–128 字节；它不是节点的共享密钥，也不验证账户所有权。
   矿工应固定**矿池证书**指纹。节点令牌只由矿池持有，不能发给矿工。

## SRBMiner-Multi

QUIC 模式，`--wallet` 对应原生协议的 worker 登录名：

```sh
SRBMiner-MULTI --disable-cpu --algorithm quantus \
  --pool POOL_IP:3333 --wallet 'YOUR_ACCOUNT.rig1' \
  --tls-cert-sha256 POOL_CERT_SHA256
```

LuckyPool Stratum 模式不传 QUIC 的证书指纹参数：

```sh
SRBMiner-MULTI --disable-cpu --algorithm quantus \
  --pool POOL_IP:3334 --wallet 'YOUR_ACCOUNT.rig1'
```

TCP/TLS 模式：

```sh
SRBMiner-MULTI --disable-cpu --algorithm quantus \
  --pool POOL_HOST:3335 --wallet 'YOUR_ACCOUNT.rig1' --tls true
```

使用同一账户、不同 `.rig` 后缀即可区分不同矿工软件的统计。

## 当前范围

- 支持原生 QUIC 以及 LuckyPool Quantus 的 `login/job/submit` 对象协议。
- 固定整数 share 难度（`diff >= 1`），`varDiff` 必须为 `null`，支持多个端口并行。
  TCP/TLS 端口的难度还必须小于 `2^64`，以适配其 uint64 难度字段。
  `diff: 1000` 是示例值，按算力调整；本地低难度测试可设为 1。
- 重用共享客户端的 share 记账、Redis、封禁和 API。每次提交后重新下发任务，
  使官方矿工继续搜索；节点断线时清理当前任务并断开矿工，随后自动重连节点。
- 新任务取消旧任务；重复 share（包括大小写不同的 nonce）拒绝记账，
  同一任务的重复通知及节点重连不会清空重复记录。
- 支持最终确认后的 PROP / SOLO 分账及自动支付，配置见下节。
  默认示例保留 `disablePayment: true`，启用付款需要配置签名钱包。
- 节点不返回 seal 接受回执、区块高度或最终区块哈希。
  日志中的 `submitted Quantus solution` 仅表示成功写入节点连接；
  支付处理器通过链 RPC 重算去除 seal 的区块头哈希，匹配已保存的候选块，
  从最终确认区块的 `MinerRewarded` 事件确认奖励。
- 节点同步期间允许矿池等待首个任务。无任务时不向矿工下发伪造工作。

## 自动支付

Quantus 挖矿奖励进入 **wormhole 地址**，不能用普通签名直接花费。
矿池从独立的、预先充值的 ML-DSA 热钱包给矿工付款；wormhole 奖励归集仍由
官方 CLI 的 `quantus wormhole collect-rewards` 完成，未集成 ZK 证明生成和
Subsquid 索引服务。不要把 wormhole 私密材料当作签名钱包 seed。

安装签名桥需要 Node.js 20+、Rust、Git 和 `wasm32-unknown-unknown`：

```sh
cd tools/quantus-wallet
npm ci --ignore-scripts
bash build-wasm.sh
cd ../..
```

官方 `@quantus-network/wasm@0.3.0` 的 npm 包缺少 `pkg/`；脚本从固定官方
提交构建缺失文件，使用其 Cargo.lock 和 wasm-bindgen 0.2.125。
每次重新执行 `npm ci` 后需再次执行构建脚本。

将**专用于此矿池**的 32 字节钱包 seed 保存为 64 位十六进制文本，文件权限
`0600`。不能与其他矿池、CLI 转账或其他 Redis 实例共用签名钱包 nonce。
Redis 必须开启 `appendonly yes`、`appendfsync always`；启动时会校验，
Redis ACL 需要允许读取这两个配置值。定期备份 Redis 账本。

在原有配置中合并以下字段（`payment` 是顶层字段）：

```json
{
  "disablePayment": false,
  "payment": { "interval": 30, "payMode": "prop" },
  "quantus": {
    "payments": {
      "rpcUrl": "http://127.0.0.1:9944",
      "genesisHash": "0x_REPLACE_WITH_CHAIN_GET_BLOCK_HASH_0",
      "rewardAddress": "REPLACE_WITH_NODE_WORMHOLE_SS58_ADDRESS",
      "walletScript": "/absolute/path/tools/quantus-wallet/wallet.mjs",
      "seedFile": "/private/path/quantus-payout.seed",
      "minPaymentUnits": "1000000000000",
      "reserveUnits": "1000000000000"
    }
  }
}
```

保留已有的 `quantus.nodeAddress`、令牌和指纹字段。`genesisHash` 使用该节点
`chain_getBlockHash(0)` 的结果；`rewardAddress` 使用节点日志中的
`Rewards wormhole address`，不是 `--rewards-inner-hash`。RPC 节点需要支持
历史事件查询（默认 canonical 状态归档即可），以及 `author_submitExtrinsic`。

金额全部为十进制整数字符串：**1 QTC = 10^12 基本单位**。示例最低付款和钱包
保留余额均为 1 QTC；最低付款与保留余额不得低于链的 existential deposit。
转账前同时检查余额、冻结金额和 RPC 估算手续费。手续费由热钱包另行承担。
矿工须以有效的 Quantus SS58 地址（网络前缀 189）登录，支持 `.rig` 后缀；
签名钱包本身不能用作矿工或手续费收款地址。

- `prop`：按前一个已确认候选块之后、当前候选块之前的有效份额难度分账。
  孤块份额并入后续有效轮次。整数除法余数归出块矿工，总金额保持精确。
- `solo`：奖励归出块矿工。两种模式都支持现有 `rewardRecipients` 百分比配置。
- 不支持 Quantus 的 PPS / PPLNS，启动会拒绝这些模式。请勿设置 Bitcoin 的
  `minPayment`，使用 `minPaymentUnits`；确认条件固定为链的最终确认，
  不使用 Bitcoin 的 `minConfirmations`。

份额和候选块在提交节点前原子写入 `COIN:quantus:ledger`。链扫描依次检查
最终确认区块的 PoW seal、含 `zkTreeRoot` 的自定义区块头和奖励事件。
付款使用 `utility.batchAll` + `balances.transferKeepAlive`，每批最多 64 个地址。
签名后的完整交易先持久化再广播；重启或 RPC 超时只重发同一交易。
最终确认的 `ExtrinsicSuccess` 才扣账；`ExtrinsicFailed` 会保留余额并暂停付款，
在 `/payments` 的 `fault` 字段报告交易哈希，需检查失败原因后处理账本。
对尚未最终确认的交易，重复广播可能返回 `Already Imported` 或 `Invalid
Transaction: Stale`；账本仍等待最终确认，不会因此另签一笔付款。

`GET /payments` 返回 12 位精度的整数字符串余额、已确认区块、付款回执和待处理
交易哈希，不暴露 seed、签名交易内容或钱包配置。完整历史保存在 Redis 账本中；
HTTP 仅返回最近 100 条回执。账本采用串行一致性写入，需按算力提高端口 share
难度以控制 Redis 写入负载，生产规模的吞吐量尚未压测。

## Verification and upstream references

```sh
go test -race ./engine/quantus ./stratum
go test -short ./...
go vet ./...
python3 scripts/e2e/quantus.py --node /path/to/quantus-node --miner /path/to/quantus-miner
```

E2E 脚本还需要本机 `redis-server`、`redis-cli`、OpenSSL、Go 和已安装的签名桥。
它使用随机本地端口、独立临时目录和公开开发种子启动真正的开发链，检查官方
矿工 QUIC 出块、TCP share、最终确认结算、实际到账以及带待处理交易崩溃重启。
测试结束只关闭它自己启动的进程；日志与 `result.json` 保留在输出的临时目录中。

Tests cover five official nonce-hash vectors, field arithmetic against `math/big`,
strict target comparison, malformed frames/jobs, certificate pinning, actual
loopback QUIC handshake/submission/reconnect, native miner message translation,
share rejection and duplicate retention. LuckyPool login/job wire fixtures were
captured read-only from `eu.lproute.com:5660` on 2026-09-12; no shares were sent
to that pool. Tests compare job fields/target encoding against those fixtures and
exercise concurrent QUIC + TCP/TLS listeners, nonce-prefix ownership, session IDs,
keepalives and Redis crediting. Payment tests cover exact u128 accounting,
round boundaries, orphan rejection, signed-intent recovery and failed extrinsics.
The real dev-chain E2E uses quantus-node 1.0.1 and the official quantus-miner 4.2.0
CPU engine on macOS/ARM, including block import and finalized transfers.
SRBMiner GPU binaries have not been executed on this machine.

Implementation references (checked 2026-09-12):

- [Node protocol and mining guide](https://github.com/Quantus-Network/chain/blob/f1176cea6a6d08ea437710dcd45cae6717b773df/MINING.md)
- [Canonical wire types](https://github.com/Quantus-Network/chain/blob/f1176cea6a6d08ea437710dcd45cae6717b773df/miner-api/src/lib.rs)
- [Consensus hash and strict U512 target comparison](https://github.com/Quantus-Network/chain/blob/f1176cea6a6d08ea437710dcd45cae6717b773df/qpow-math/src/lib.rs)
- [Official miner vectors](https://github.com/Quantus-Network/quantus-miner/blob/c1cf0a37dce77f98df3c73c390f958d7efddaf85/crates/pow-core/src/lib.rs)
- [Official ML-DSA signing library](https://github.com/Quantus-Network/quantus-wasm/tree/539e3474e14d0dd5c2039c95e77e996e9d12bf93)
- [Mining reward events](https://github.com/Quantus-Network/chain/blob/f1176cea6a6d08ea437710dcd45cae6717b773df/pallets/mining-rewards/src/lib.rs)
- `qp-poseidon-core` 3.1.0 parameters and sponge encoding; MIT attribution is
  retained in [LICENSE.poseidon](LICENSE.poseidon). Official miner test vectors
  retain their Apache-2.0 license in [testdata/LICENSE](testdata/LICENSE).

The hash consumes `mining_hash[32] || nonce[64]`, encodes little-endian u32 input
limbs, adds the serialization terminator and sponge padding, and squeezes twice.
Output field limbs are little-endian u64; the complete 64-byte hash is interpreted
as big-endian. Network validity is strictly `hash < (2^512 - 1) / difficulty`.

## LuckyPool wire format

TCP/TLS uses newline-delimited JSON. `login` params include `login`, `pass` and
`agent`. The result contains `id` (session ID), `status: "OK"`,
`extensions: ["keepalive"]`, and the initial `job` (or null while waiting).
Jobs contain `algo: "qpow-poseidon2"`, `job_id`, `mining_hash`, `extranonce`
(4-byte hex), `target` (64-byte big-endian hex), integer `difficulty`, and `seq`.

Subsequent notifications are JSON-RPC 2.0 messages with method `job` and params
`{"clean_jobs": true, "job": {...}}`, without an `id` field. Each connection
retains its own extranonce on every new job. `submit` params contain `id`,
`job_id`, and `nonce` (64 bytes of hex, starting with the assigned extranonce).
The result is `{"status":"OK"}` for accepted shares; `keepalived` returns
`{"status":"KEEPALIVED"}`. Session ID, prefix, nonce size, job freshness, PoW
and duplicates are all checked before a share is credited.
