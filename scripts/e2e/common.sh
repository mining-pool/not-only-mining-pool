#!/usr/bin/env bash
# Shared helpers for the reproducible end-to-end suite. Sourced by the runners.
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="${E2E_WORK:-/tmp/nomp-e2e}"
BIN="$WORK/bin"
mkdir -p "$WORK" "$BIN"
export PATH="$BIN:$PATH"

log()  { printf '\033[1;36m[e2e]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[e2e] ✅ %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31m[e2e] ❌ %s\033[0m\n' "$*"; }
skip() { printf '\033[1;33m[e2e] ⏭  %s\033[0m\n' "$*"; }

have() { command -v "$1" >/dev/null 2>&1; }

# free_port kills whatever holds a TCP port (leftover from a previous run).
free_port() { lsof -ti :"$1" 2>/dev/null | xargs kill -9 2>/dev/null; true; }

# ensure_redis starts a throwaway redis on 6379 if none is reachable.
ensure_redis() {
  redis-cli ping >/dev/null 2>&1 && return 0
  have redis-server || { fail "redis-server not on PATH"; return 1; }
  redis-server --daemonize yes --port 6379 --save '' --appendonly no >"$WORK/redis.log" 2>&1
  sleep 1; redis-cli ping >/dev/null 2>&1
}

# build_pool <outfile> [tags]  — build the pool binary (cgo on when tags given).
build_pool() {
  local out="$1" tags="${2:-}"
  ( cd "$REPO" && CGO_ENABLED=1 GOTOOLCHAIN=local go build ${tags:+-tags "$tags"} -o "$out" . )
}

# build_tool <dir> <outfile> [tags]
build_tool() {
  local dir="$1" out="$2" tags="${3:-}"
  ( cd "$REPO" && CGO_ENABLED=1 GOTOOLCHAIN=local go build ${tags:+-tags "$tags"} -o "$out" "./tools/$dir/" )
}

# json_field <jsonfile> <python-expr on d>  — extract a field via python3.
json_field() { python3 -c "import sys,json;d=json.load(open('$1'));print($2)"; }

# track a pid/process for cleanup
E2E_PIDS=()
track()   { E2E_PIDS+=("$1"); }
cleanup() { for p in "${E2E_PIDS[@]:-}"; do kill -9 "$p" 2>/dev/null; done; }

# retry_rpc <url> <jsonbody> — curl a JSON-RPC endpoint, echo body, non-zero on failure.
rpc() { curl -s -m 8 "$1" -H 'Content-Type: application/json' -d "$2"; }

# with_timeout <secs> <cmd...> — bound a blocking command (miner brute-force,
# wallet gRPC call) so a stuck daemon fails fast instead of hanging the suite.
# Falls back to running without a limit where `timeout` is absent (bare macOS).
with_timeout() { local s="$1"; shift; if have timeout; then timeout "$s" "$@"; else "$@"; fi; }

# wait_pool_started <poollog> — return 0 once the pool logs it is up (waits ≤30s).
# Callers report their own failure diagnostics (each tails a different amount).
wait_pool_started() {
  for i in $(seq 1 30); do grep -q "Stratum Pool Server Started" "$1" 2>/dev/null && return 0; sleep 1; done
  return 1
}
