# 币种配置模板 / Coin config templates

每个 `config.<coin>.json` 是一个可直接使用的模板。用法：

```bash
cp coins/config.litecoin.json ../config.json   # 或拷到你运行目录
# 编辑 config.json：换钱包地址、daemon RPC、redis
go build . && ./not-only-mining-pool -c config.json
```

**必须修改的字段**：`poolAddress.address`、`rewardRecipients[].address`（换成你自己的地址）、
`daemons[]`（你的全节点 RPC）、`storage`（你的 Redis）。

完整说明见仓库根目录 [`TUTORIAL_zh.md`](../TUTORIAL_zh.md)。

| 模板 | 算法 | 状态 |
|------|------|------|
| config.bitcoin.json / bitcoincash / bitcoinsv | sha256d | ✅ 开箱即用 |
| config.litecoin.json / dogecoin.json | scrypt | ✅ 开箱即用 |
| config.dash.json | x11 | ✅ 开箱即用 |
| config.digibyte.json / namecoin.json / peercoin.json | sha256d | ✅ 开箱即用 |
| config.maxcoin.json | keccak | ✅ 开箱即用 |
| config.groestlcoin.json | groestl（区块 ID 为单轮 sha256）| ✅ 开箱即用（上线前先跑 regtest 回归）|
| config.vertcoin.json | verthash（首次生成 ~1.2GB verthash.dat）| ✅ 开箱即用 |
| config.monacoin.json | lyra2rev2 | ✅ 开箱即用 |
| config.ecash.json / syscoin.json | sha256d | ✅ 开箱即用（地址请用 legacy base58 格式）|
| config.viacoin.json / einsteinium.json | scrypt | ✅ 开箱即用 |
| config.feathercoin.json | neoscrypt | ⚙️ 需 cgo 绑定（无纯 Go 实现）|

⚙️ 标记的算法请参照 `TUTORIAL_zh.md` 第 4 节用 `algorithm.RegisterHash` 绑定后使用。
`daemons[].port` 为各币 mainnet 默认 RPC 端口，请按实际 `*.conf` 核对。
