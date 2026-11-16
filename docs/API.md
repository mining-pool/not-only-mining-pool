# HTTP API, administrator login and the web portal

[中文](API_zh.md)

The NOMP Go process serves JSON APIs only; it does not host HTML or static
assets. The pool website lives in a separate repository,
[mining-pool/pool-webpage](https://github.com/mining-pool/pool-webpage) (a React
single-page app). It is built and deployed on its own and talks to the pool
through the endpoints described here.

```text
browser ──► web portal  https://pool.example         (pool-webpage dist/, any static host)
   │
   └──────► pool API    https://api.pool.example:20000 (this repo's nomp process, "api" section)
```

One portal build serves one pool instance; for several coins, deploy one
portal and API per domain.

## Quick start: local development

1. Run the pool (this repository):

   ```bash
   go build -o nomp ./cmd/nomp
   umask 077
   ./nomp -hash-admin-password > admin-password.hash   # optional, only for the admin console
   ```

   Merge this `api` section into `config.json` (coin, daemons, ports and Redis
   as usual), then run `./nomp -c config.json`:

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

2. Run the portal (in another directory):

   ```bash
   git clone https://github.com/mining-pool/pool-webpage.git
   cd pool-webpage
   npm ci
   cp .env.example .env.local
   npm run dev          # http://127.0.0.1:5173, proxies /api and /admin-api to 127.0.0.1:20000
   ```

   Without a node or Redis, `npm run dev:demo` previews the UI with built-in
   sample data, or run the API test fixture instead of `nomp`. It serves the
   real API (storage, admin sessions, CORS, privacy rules) on an in-memory
   Redis seeded with shares; it is for development and tests only:

   ```bash
   NOMP_FIXTURE_ADMIN_PASSWORD=dev-password-123 \
     go run ./scripts/web-fixture -addr 127.0.0.1:20000 -origin http://127.0.0.1:5173
   ```

Portal environment variables, UI notes and static deployment are covered in the
[pool-webpage README](https://github.com/mining-pool/pool-webpage#readme).

## Public endpoints

| Endpoint | Purpose |
| --- | --- |
| `GET /api/` | Capability list (registered paths); clients use it to detect optional endpoints |
| `GET /api/pool` | Work rates over five windows (1m / 30m / 1h / 6h / 24h) and the miner count `minerCount` |
| `GET /api/config` | List of readable configuration keys |
| `GET /api/config/coin`, `/api/config/algorithm`, `/api/config/ports` | Coin, algorithm and port information (`ports.*.tls` is a boolean; certificate paths are never returned) |
| `GET /api/config/api` | API host and port only |
| `GET /api/miner/{miner}` | Miner work rates, round contribution and historical rig index |
| `GET /api/miner/{miner}/rig/{rig}` | Per-rig window data |
| `GET /api/payments` (optional) | Registered when payments are enabled (currently Quantus): finalized height, pending batch, settled blocks |
| `GET /api/payments?address={miner}` | The same, plus that address's own balance, pending outputs and finalized receipts |

### Privacy

The pool does not publish its miner list; public endpoints only answer for a
full address the caller already knows:

- `/api/pool` returns only the miner count `minerCount`, never the `miners`
  address list.
- `/api/payments` without `address` returns empty `balances` and `paid`, and
  keeps only the `id` and `hash` of a pending batch (no `outputs`). With
  `address` it returns only that address's balance, outputs and receipts;
  other miners' addresses and amounts are filtered out. The internal `fault`
  detail is replaced by a generic notice.
- Through an administrator session, `/admin-api/pool` and
  `/admin-api/payments` return the full `miners` list and payout ledger.
- Filtering happens in the API layer on a shared payments shape, so a new
  payment engine gets the same protection by returning the same fields
  (`balances`, `pending.outputs`, `paid[].outputs`).

Data semantics:

- `hashrate*` fields are hashrates estimated from accepted shares over each
  window: Σ(credited share difficulty) × work per difficulty ÷ window seconds,
  in `hashrateUnit` (`H/s`, or `Sol/s` for Equihash and Beam). Shares are
  credited at their assigned difficulty, never the luckier achieved one, so a
  block-solving share does not spike the rate. Samples are kept for 25 hours.
  Work per difficulty is 2^256 ÷ the difficulty-1 target the shares are checked
  against:

  | Engine | Work per difficulty | Unit |
  | --- | --- | --- |
  | GBT coins | 2^256 ÷ (0xFFFF·2^208 · 2^`algorithm.multiplier`) ≈ 2^32 ÷ 2^multiplier | H/s |
  | ethash, kaspa, cryptonote (RandomX), ergo, alephium, quantus | 1 | H/s |
  | kawpow | 2^40 ÷ 255 | H/s |
  | equihash / zelhash | 2^32 ÷ 0x7FFFF ≈ 8192 | Sol/s |
  | beam | 1 per credited difficulty (solutions/s when ports use difficulty 1) | Sol/s |

  If your miners use a different difficulty convention, set
  `api.hashesPerDifficulty` to override the factor.
- The five values are windows of different lengths, not a time series. Miner
  and rig indexes are historical, not online counts.
- Go `nil` collections serialize as `null`; the portal treats them as empty.
- Older clients that relied on the `miners` list from `/pool` must use the
  admin endpoint instead.
- Payment amounts are base-unit integer strings (10^12 per QTC); clients must
  not convert them to floating point. Only the latest 100 receipts are kept.
- Configuration endpoints return public fields only — never the admin
  settings, password hash path or TLS configuration.

### Legacy paths

The original `/`, `/pool`, `/miner/:address`, `/config/*` and `/payments` paths
keep their JSON semantics and never switch to HTML based on `Accept`; `/api/*`
are aliases. Pool endpoints accept only GET / HEAD; login and logout use POST.
Page paths such as `/admin` or `/index.html` return 404 — the admin UI lives at
`/admin` on the portal site.

## Administrator account

```bash
umask 077
./nomp -hash-admin-password > admin-password.hash
```

The command reads and confirms the password from the terminal without echo and
prints a bcrypt hash. Passwords must be 12–72 bytes. Keep the hash file on the
API server only, never in the portal build or a repository. There is no
built-in password and no sign-up; without `api.admin` the admin endpoints
return 503 while public lookups keep working.

`api.admin` fields:

| Field | Meaning |
| --- | --- |
| `username` | Administrator account name |
| `passwordHashFile` | Path to the bcrypt hash; relative paths resolve against the process working directory |
| `sessionTTLSeconds` | Absolute session lifetime, 60–86400, default 28800 (8 h) |
| `cookieSameSite` | `strict` (default); `none` for cross-site portal/API deployments (HTTPS required) |
| `allowInsecureHTTP` | Local development only: allows HTTP login when both the Host and the peer are loopback |

`allowInsecureHTTP` is not a switch for public HTTP login. Go does not trust
`X-Forwarded-For` / `X-Forwarded-Proto`.

### Session endpoints

| Endpoint | Behaviour |
| --- | --- |
| `POST /admin-api/auth/login` | JSON `{username,password}`; returns the account, expiry and a CSRF token and sets the session cookie |
| `GET /admin-api/auth/session` | Current session; 401 when absent or expired |
| `POST /admin-api/auth/logout` | Checks `X-CSRF-Token`, destroys the session and clears the cookie |
| `GET` / `POST /admin-api/reload` | Last configuration reload / reload now (POST needs `X-CSRF-Token`), see [Configuration reload](#configuration-reload) |
| `GET` / `PUT /admin-api/settings` | Read / write and apply the reloadable settings (PUT needs `X-CSRF-Token`), see [Editing settings through the API](#editing-settings-through-the-api) |
| `/admin-api/*` | Pool API behind the session (same paths as `/api/*`, including the full miner index and payout ledger; currently read-only) |

- The session cookie is HttpOnly, Secure over HTTPS, scoped to `/admin-api`.
  Logging in again rotates the session.
- Sessions live in process memory (at most 256); logout takes effect
  immediately and a restart signs every administrator out.
- Login requires JSON and an allowed Origin; logout and future admin writes
  also require `X-CSRF-Token`.
- Login is limited to 10 attempts per peer per 5 minutes, with bounded
  concurrent password checks.
- Authorization is enforced in Go, not by hiding portal routes. New sensitive
  operations must be mounted under `/admin-api/`, never on public routes.

## Configuration reload

The pool re-reads its configuration file without a restart when it receives
`SIGHUP`, or when an administrator clicks **Reload configuration** in the web
portal (`POST /admin-api/reload`, which needs the session and CSRF token and
re-reads the file on the server). To change settings over the API instead of
editing the file, see [Editing settings through the API](#editing-settings-through-the-api).

```bash
kill -HUP "$(pidof nomp)"
# systemd: ExecReload=/bin/kill -HUP $MAINPID, then
systemctl reload nomp
```

A reload is all-or-nothing. The new file is parsed and validated first —
certificates are loaded, new ports are bound, the administrator hash and
reward addresses are checked, and each mining engine validates its own
settings with the same rules it uses at startup. If anything fails, or the file changes a setting
that needs a restart, nothing is applied and the pool keeps running as before.
The outcome is logged and shown in the portal (`GET /admin-api/reload` returns
the last result).

| Setting | Applied |
| --- | --- |
| `ports`: add a port | Starts listening immediately |
| `ports`: remove a port | Stops listening and disconnects miners on it (they fail over per their own configuration) |
| `ports`: `diff`, `varDiff` | New connections; connected miners keep their settings until they reconnect |
| `ports`: TLS certificate renewed or paths changed | New handshakes; no connection is dropped (also for QUIC) |
| `ports`: TLS switched on/off, or `protocol` changed | The port is rebound and its connections dropped |
| `banning` (including enabling/disabling) | Immediately |
| `connectionTimeout`, `tcpProxyProtocol` | Next check / new connections |
| `rewardRecipients` | From the next job (GBT coins, kawpow) or the next payout run (quantus). Other engines' nodes build the coinbase, so fee recipients do not apply to them |
| `payment.interval`, `minPayment`, `minConfirmations` | From the next payout run; a run in progress is never changed |
| `quantus.payments.minPaymentUnits`, `reserveUnits` | From the next payout run (still checked against the chain's existential deposit) |
| `api.allowedOrigins`, `api.hashesPerDifficulty` | Immediately |
| `api.admin` | Immediately; every administrator session is signed out |
| `api.tls` certificate renewed or paths changed | New handshakes |

These need a restart and make a reload fail: `coin`, `engine`, `algorithm`,
`daemons`, `p2p`, `storage`, `poolAddress`, the rest of `quantus`, `disablePayment`,
`blockRefreshInterval`, `jobRebroadcastTimeout`, `emitInvalidBlockHashes`,
`api.host`, `api.port`, enabling or disabling `api.tls`, and every `payment`
field other than the three above (the pay mode and accounting parameters stay
fixed so one ledger never mixes rules).

### Editing settings through the API

`GET /admin-api/settings` returns the editable part of `config.json` on disk —
exactly the settings in the table above — with a `version` (a hash of the file)
and `inForce` (whether the file on disk is what is running; `false` after an
edit on the server that was not reloaded).

`PUT /admin-api/settings` with `{"version": "...", "settings": {...}}` (and the
session cookie and `X-CSRF-Token`) changes them:

1. The version must still match the file, so a concurrent edit — another
   administrator or a manual change on the server — is never overwritten
   (`409 settings_conflict`).
2. Only the editable keys are replaced; every other setting, and the order of
   keys, stays as it was. Unknown fields are rejected (`400 settings_invalid`).
3. The whole resulting configuration is validated like a reload. If it is
   rejected, nothing is written (`422` with the reload report).
4. Otherwise the previous file is kept as `config.json.bak`, the new one is
   written atomically, and it is applied (`200` with the report).

Every attempt is appended to `config.json.audit.log` (administrator, address,
changes, errors). The web portal's **Edit configuration** panel uses this API.

```bash
curl -s -b cookies -c cookies -H 'Origin: https://pool.example' \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"..."}' https://api.pool.example/admin-api/auth/login
# → {"csrfToken": "..."}; then
curl -s -b cookies -H 'Origin: https://pool.example' https://api.pool.example/admin-api/settings > settings.json
# edit .settings, then send it back with the version you read:
curl -s -b cookies -X PUT -H 'Origin: https://pool.example' -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' -d @request.json https://api.pool.example/admin-api/settings
```

These endpoints can change the administrator account and certificate paths;
protect the administrator password accordingly.

## Production deployment

Go can serve the API over HTTPS directly:

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

Build the portal against it (include the port unless it is 443):

```bash
VITE_API_BASE_URL=https://api.pool.example:20000 npm run build
```

### CORS and cookies

- `allowedOrigins` is an exact list of origins including scheme and port; `*`
  and paths are rejected. Only listed origins receive credentialed CORS
  responses (including preflight); unknown origins get 403. Same-origin
  requests and clients without an `Origin` header (curl, monitoring) are
  unaffected. Admin writes always require an allowed Origin.
- The default `SameSite=Strict` suits same-site deployments such as
  `https://pool.example` + `https://api.pool.example`. For entirely different
  sites set `cookieSameSite: "none"` (HTTPS only, incompatible with
  `allowInsecureHTTP`); browser third-party cookie policies may still block
  login, so prefer same-site domains in production.
- The certificate must match the API domain. Invalid hash files, certificates
  or origin settings fail at startup.

## Verification

```bash
go test -race ./api
go test -short ./...
```

The `api` tests cover privacy filtering of public endpoints, login, wrong passwords, cookie attributes, session
rotation and expiry, logout invalidation, CSRF, CORS preflight and allow-list,
login rate limiting, HTTP origin restrictions, config redaction and API-only
routing. `storage` tests cover work-rate windows, equal-difficulty shares and
sample trimming. The portal's browser tests (pool-webpage `npm run e2e`) run
the production build against `scripts/web-fixture`.
