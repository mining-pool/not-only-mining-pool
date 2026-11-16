# HTTP API、管理员登录与网页前端

[English](API.md)

NOMP 的 Go 进程只提供 JSON API，不托管 HTML 或静态文件。矿池网页是独立仓库
[mining-pool/pool-webpage](https://github.com/mining-pool/pool-webpage)（React 单页应用），单独构建、单独部署，通过本文描述的接口与矿池通信。

```text
浏览器 ──► 前端站点 https://pool.example        （pool-webpage 构建出的 dist/，任意静态托管）
   │
   └────► 矿池 API  https://api.pool.example:20000（本仓库 nomp 进程，api 配置段）
```

一套前端对应一个矿池实例；多币种按域名分别部署前端和 API。

## 快速开始：本机联调

1. 启动矿池（本仓库）：

   ```bash
   go build -o nomp ./cmd/nomp
   umask 077
   ./nomp -hash-admin-password > admin-password.hash   # 可选，仅管理控制台需要
   ```

   在 `config.json` 中合并以下 `api` 段（其他币种、节点、端口、Redis 参数照常设置），然后 `./nomp -c config.json`：

   ```json
   {
     "api": {
       "host": "127.0.0.1",
       "port": 20000,
       "allowedOrigins": ["http://127.0.0.1:5173"],
       "admin": {
         "username": "admin",
         "passwordHashFile": "admin-password.hash",
         "sessionTTLSeconds": 28800,
         "allowInsecureHTTP": true
       }
     }
   }
   ```

2. 启动前端（另一个目录）：

   ```bash
   git clone https://github.com/mining-pool/pool-webpage.git
   cd pool-webpage
   npm ci
   cp .env.example .env.local
   npm run dev          # http://127.0.0.1:5173，/api 与 /admin-api 代理到 127.0.0.1:20000
   ```

   没有节点或 Redis 时，`npm run dev:demo` 可用内置演示数据预览界面；也可以用 API 测试服务代替 `nomp`，它用预先写入份额的内存 Redis 提供真实 API（存储、管理员会话、CORS、隐私规则），仅用于开发和测试：

   ```bash
   NOMP_FIXTURE_ADMIN_PASSWORD=dev-password-123 \
     go run ./scripts/web-fixture -addr 127.0.0.1:20000 -origin http://127.0.0.1:5173
   ```

前端的环境变量、界面说明和静态部署方式见 [pool-webpage README](https://github.com/mining-pool/pool-webpage#readme)。

## 公开接口

| 接口 | 用途 |
| --- | --- |
| `GET /api/` | 能力列表（已注册的路径），前端据此判断可选接口 |
| `GET /api/pool` | 五个窗口（1 分钟 / 30 分钟 / 1 小时 / 6 小时 / 24 小时）的工作量与矿工数量 `minerCount` |
| `GET /api/config` | 可读取的配置项列表 |
| `GET /api/config/coin`、`/api/config/algorithm`、`/api/config/ports` | 币种、算法和端口信息（`ports.*.tls` 为布尔值，不返回证书路径） |
| `GET /api/config/api` | 仅返回 API 的 host 与 port |
| `GET /api/miner/{miner}` | 矿工工作量、本轮贡献、历史矿机索引 |
| `GET /api/miner/{miner}/rig/{rig}` | 单台矿机的窗口数据 |
| `GET /api/payments`（可选） | 支付启用时注册（目前为 Quantus）：已确认高度、待确认批次、结算区块 |
| `GET /api/payments?address={miner}` | 同上，另加该地址自己的余额、待确认输出和已确认回执 |

### 隐私

矿池不公开矿工名单，公开接口只能查询调用方已知的完整地址：

- `/api/pool` 只返回矿工数量 `minerCount`，不返回 `miners` 地址列表。
- `/api/payments` 不带 `address` 时，`balances` 为空对象、`paid` 为空数组，待确认批次只保留 `id` 与 `hash`（`outputs` 为空）。带 `address` 时只返回该地址的余额、输出和回执，其他矿工的地址与金额被过滤。内部故障详情 `fault` 不会公开，只返回一句通用提示。
- 通过管理员会话访问 `/admin-api/pool`、`/admin-api/payments` 时返回完整的 `miners` 列表和支付账本。
- 支付过滤在 API 层按统一的支付结构完成，新的支付引擎只需按相同字段（`balances`、`pending.outputs`、`paid[].outputs`）返回即可获得同样的保护。

数据语义：

- `hashrate*` 字段是根据各窗口内已接受份额估算的算力：Σ(计入的份额难度) × 每单位难度的工作量 ÷ 窗口秒数，单位见 `hashrateUnit`（`H/s`；Equihash 与 Beam 为 `Sol/s`）。份额按分配难度计入，而不是实际碰到的更高难度，因此出块份额不会造成算力尖峰。样本保留 25 小时。每单位难度的工作量 = 2^256 ÷ 校验份额所用的难度 1 目标值：

  | 引擎 | 每单位难度工作量 | 单位 |
  | --- | --- | --- |
  | GBT 币种 | 2^256 ÷ (0xFFFF·2^208 · 2^`algorithm.multiplier`) ≈ 2^32 ÷ 2^multiplier | H/s |
  | ethash、kaspa、cryptonote（RandomX）、ergo、alephium、quantus | 1 | H/s |
  | kawpow | 2^40 ÷ 255 | H/s |
  | equihash / zelhash | 2^32 ÷ 0x7FFFF ≈ 8192 | Sol/s |
  | beam | 每单位计入难度 1（端口难度为 1 时即 solutions/s） | Sol/s |

  如果矿机使用不同的难度约定，可通过 `api.hashesPerDifficulty` 覆盖该系数。
- 五个值是不同长度的统计窗口，不是历史时间序列。矿工 / 矿机索引是历史记录，不等于在线数量。
- Go 中为 `nil` 的集合会序列化为 `null`，前端将其视为空数组 / 对象。
- 旧客户端如果依赖 `/pool` 返回的 `miners` 列表，需要改用管理员接口。
- 支付金额是最小单位的整数字符串（QTC 为 10^12），客户端不应转为浮点数。支付接口只保留最近最多 100 笔回执。
- 配置接口只返回公开字段，绝不返回管理员配置、密码哈希文件位置或 TLS 配置。

### 旧路径兼容

原有 `/`、`/pool`、`/miner/:address`、`/config/*`、`/payments` 等路径保持 JSON 语义，不会根据 `Accept` 返回页面；`/api/*` 是它们的别名。矿池 API 只接受 GET / HEAD，登录与退出使用 POST。访问 `/admin`、`/index.html` 等页面路径返回 404——管理入口在前端站点的 `/admin`。

## 管理员账号

```bash
umask 077
./nomp -hash-admin-password > admin-password.hash
```

命令从终端读取并确认密码（不回显），输出 bcrypt 哈希。密码需为 12–72 字节。哈希文件只保存在 API 服务器上，不要放进前端构建产物或仓库。没有内置管理员密码，也没有注册入口；未配置 `api.admin` 时管理接口返回 503，公开查询照常可用。

`api.admin` 字段：

| 字段 | 说明 |
| --- | --- |
| `username` | 管理员账号 |
| `passwordHashFile` | bcrypt 哈希文件路径；相对路径以启动进程的工作目录为基准 |
| `sessionTTLSeconds` | 会话绝对有效期，60–86400，默认 28800（8 小时） |
| `cookieSameSite` | 默认 `strict`；前端与 API 跨站部署时设为 `none`（必须 HTTPS） |
| `allowInsecureHTTP` | 仅限本机开发：允许 Host 与连接来源均为回环地址的 HTTP 登录 |

`allowInsecureHTTP` 不是公网 HTTP 登录开关。Go 不信任 `X-Forwarded-For` / `X-Forwarded-Proto`。

### 会话接口

| 接口 | 行为 |
| --- | --- |
| `POST /admin-api/auth/login` | JSON `{username,password}`，返回账号、到期时间和 CSRF token，并设置会话 Cookie |
| `GET /admin-api/auth/session` | 当前会话；未登录或到期返回 401 |
| `POST /admin-api/auth/logout` | 校验 `X-CSRF-Token` 后销毁会话并清除 Cookie |
| `GET` / `POST /admin-api/reload` | 上次配置重载结果 / 立即重载（POST 需要 `X-CSRF-Token`），见[配置热重载](#配置热重载) |
| `GET` / `PUT /admin-api/settings` | 读取 / 写入并应用可热修改的配置（PUT 需要 `X-CSRF-Token`），见[通过 API 修改配置](#通过-api-修改配置) |
| `/admin-api/*` | 登录后访问的矿池 API（与 `/api/*` 相同的路径，包含完整矿工索引与支付账本，当前只读） |

- 会话 Cookie 为 HttpOnly，HTTPS 下带 Secure，Path 限于 `/admin-api`。重新登录会轮换会话。
- 会话只存在进程内存（最多 256 个），退出立即失效，服务重启后需重新登录。
- 登录要求 JSON 与合法 Origin；退出和以后的管理写接口还要求 `X-CSRF-Token`。
- 登录按连接来源限制为每 5 分钟最多 10 次，并限制并发密码校验。
- 权限在 Go 端校验，不依赖前端隐藏路由。以后新增敏感运维接口必须挂在 `/admin-api/` 下，不能放进公开路由。

## 配置热重载

矿池收到 `SIGHUP`，或管理员在网页后台点击**重新加载配置**（`POST /admin-api/reload`，需要管理员会话和 CSRF token，重新读取服务器上的文件）时，会在不重启的情况下重新读取配置文件。若想通过 API 直接修改配置而不是编辑文件，见[通过 API 修改配置](#通过-api-修改配置)。

```bash
kill -HUP "$(pidof nomp)"
# systemd：配置 ExecReload=/bin/kill -HUP $MAINPID 后
systemctl reload nomp
```

重载要么全部生效，要么全部不生效。会先解析并校验新文件：加载证书、绑定新端口、检查管理员密码哈希和收款地址，各挖矿引擎还会用与启动时相同的规则校验自己的配置。任何一步失败，或文件修改了必须重启的配置，都不会应用任何改动，矿池照常运行。结果会写入日志并显示在网页后台（`GET /admin-api/reload` 返回上次结果）。

| 配置 | 生效方式 |
| --- | --- |
| `ports`：新增端口 | 立即开始监听 |
| `ports`：删除端口 | 停止监听并断开该端口上的矿机（矿机按自身配置切换） |
| `ports`：`diff`、`varDiff` | 新连接生效；已连接的矿机保持原设置，重连后生效 |
| `ports`：TLS 证书续期或更换路径 | 新握手生效，不断开任何连接（QUIC 同样） |
| `ports`：开关 TLS 或修改 `protocol` | 重新绑定端口，该端口上的连接会断开 |
| `banning`（含开启 / 关闭） | 立即生效 |
| `connectionTimeout`、`tcpProxyProtocol` | 下次检查 / 新连接生效 |
| `rewardRecipients` | 下一个挖矿任务（GBT 币种、kawpow）或下一轮结算（quantus）开始生效。其他引擎的 coinbase 由节点构建，手续费收款方对它们不适用 |
| `payment.interval`、`minPayment`、`minConfirmations` | 下一轮结算开始生效，绝不改动进行中的结算 |
| `quantus.payments.minPaymentUnits`、`reserveUnits` | 下一轮结算开始生效（仍会检查不低于链上最小存款） |
| `api.allowedOrigins`、`api.hashesPerDifficulty` | 立即生效 |
| `api.admin` | 立即生效，所有管理员会话退出登录 |
| `api.tls` 证书续期或更换路径 | 新握手生效 |

以下配置必须重启，修改后重载会失败：`coin`、`engine`、`algorithm`、`daemons`、`p2p`、`storage`、`poolAddress`、`quantus` 的其余字段、`disablePayment`、`blockRefreshInterval`、`jobRebroadcastTimeout`、`emitInvalidBlockHashes`、`api.host`、`api.port`、开启或关闭 `api.tls`，以及 `payment` 中除上述三项外的所有字段（结算模式和记账参数保持不变，保证同一账本不会混用规则）。

### 通过 API 修改配置

`GET /admin-api/settings` 返回磁盘上 `config.json` 中可编辑的部分（即上表中的配置），以及 `version`（文件内容的哈希）和 `inForce`（磁盘上的文件是否就是当前运行的配置；在服务器上手工修改但未重载时为 `false`）。

`PUT /admin-api/settings` 提交 `{"version": "...", "settings": {...}}`（需要会话 Cookie 和 `X-CSRF-Token`）来修改配置：

1. 版本号必须与当前文件一致，因此不会覆盖其他管理员或服务器上的手工修改（`409 settings_conflict`）。
2. 只替换可编辑的键，其他配置和键的顺序保持不变；未知字段会被拒绝（`400 settings_invalid`）。
3. 修改后的整份配置按重载流程校验，被拒绝时不会写入任何内容（`422`，附重载报告）。
4. 校验通过后，原文件保存为 `config.json.bak`，新文件原子写入并立即生效（`200`，附报告）。

每次修改尝试都会追加到 `config.json.audit.log`（管理员、来源地址、改动、错误）。网页后台的**编辑配置**面板使用的就是这个接口。

```bash
curl -s -b cookies -c cookies -H 'Origin: https://pool.example' \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"..."}' https://api.pool.example/admin-api/auth/login
# 返回 {"csrfToken": "..."}，然后：
curl -s -b cookies -H 'Origin: https://pool.example' https://api.pool.example/admin-api/settings > settings.json
# 修改 .settings 后，连同读取到的 version 一起提交：
curl -s -b cookies -X PUT -H 'Origin: https://pool.example' -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' -d @request.json https://api.pool.example/admin-api/settings
```

这些接口可以修改管理员账号和证书路径，请妥善保护管理员密码。

## 生产部署

API 可以由 Go 直接提供 HTTPS：

```json
{
  "api": {
    "host": "0.0.0.0",
    "port": 20000,
    "allowedOrigins": ["https://pool.example"],
    "tls": {
      "certFile": "/srv/nomp/tls/fullchain.pem",
      "keyFile": "/srv/nomp/tls/privkey.pem"
    },
    "admin": {
      "username": "admin",
      "passwordHashFile": "/srv/nomp/admin-password.hash",
      "sessionTTLSeconds": 28800
    }
  }
}
```

前端构建时指向这个 API（端口非 443 时需带上端口）：

```bash
VITE_API_BASE_URL=https://api.pool.example:20000 npm run build
```

### CORS 与 Cookie

- `allowedOrigins` 是包含协议和端口的精确 origin 列表，不接受 `*` 或路径。只有许可的 origin 会得到携带凭证的 CORS 响应（含预检）；未知 origin 返回 403。同源请求和不带 `Origin` 的普通客户端（curl、监控）不受影响。管理写请求始终要求合法 Origin。
- 默认 `SameSite=Strict`，适用于同站点部署，例如 `https://pool.example` + `https://api.pool.example`。完全不同站点时设 `cookieSameSite: "none"`（需要 HTTPS，且不能同时开启 `allowInsecureHTTP`）；浏览器的第三方 Cookie 策略仍可能阻止登录，生产优先使用同站点域名。
- 证书需与 API 域名匹配。无效的哈希文件、证书或 origin 配置会在启动时报错退出。

## 验证

```bash
go test -race ./api
go test -short ./...
```

`api` 包的测试覆盖公开接口的隐私过滤、登录、错误密码、Cookie 属性、会话轮换与到期、退出后失效、CSRF、CORS 预检与白名单、登录限流、HTTP 来源限制、配置脱敏和纯 API 路由。`storage` 测试覆盖工作量窗口、相同难度份额和样本裁剪。前端的浏览器测试（pool-webpage `npm run e2e`）用生产构建对接 `scripts/web-fixture` 运行。
