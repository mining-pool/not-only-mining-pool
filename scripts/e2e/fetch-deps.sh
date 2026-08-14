#!/usr/bin/env bash
# Fetch coin daemons into $E2E_WORK/bin for a BARE-HOST run (no Docker). Detects
# OS + arch and downloads the matching release binaries. The supported, fully
# reproducible path is the Docker image (scripts/e2e/Dockerfile); this helper is
# a convenience for running the suite directly on a dev machine.
#
# redis-server and the Go toolchain must already be on PATH.
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

case "$(uname -s)" in Darwin) OS=osx;; Linux) OS=linux;; *) fail "unsupported OS"; exit 1;; esac
case "$(uname -m)" in x86_64|amd64) M64=x86_64;; arm64|aarch64) M64=aarch64;; *) M64=x86_64;; esac
log "host: OS=$OS arch=$M64"

dl() { # <url> <bin...>
  local url="$1"; shift
  [ -z "$url" ] && return 1
  local f="$WORK/dl.tar.gz"
  curl -fsSL -o "$f" "$url" 2>/dev/null || { skip "download failed: $url"; return 1; }
  tar tzf "$f" >/dev/null 2>&1 || { skip "not an archive: $url"; return 1; }
  tar xzf "$f" -C "$WORK"
  for b in "$@"; do
    local p; p=$(find "$WORK" -type f -name "$b" -perm -u+x 2>/dev/null | head -1)
    [ -n "$p" ] && cp "$p" "$BIN/$b" && log "installed $b"
  done
}

# platform-specific asset suffix
if [ "$OS" = osx ]; then SUF_BTC="${M64/aarch64/arm64}-apple-darwin"; GH_SUF="${M64/x86_64/x86_64}-apple-darwin"
else SUF_BTC="${M64}-linux-gnu"; GH_SUF="${M64}-linux-gnu"; fi

have bitcoind     || dl "https://bitcoincore.org/bin/bitcoin-core-27.0/bitcoin-27.0-${SUF_BTC}.tar.gz" bitcoind bitcoin-cli
have groestlcoind || dl "https://github.com/Groestlcoin/groestlcoin/releases/download/v31.0/groestlcoin-31.0-${GH_SUF}.tar.gz" groestlcoind groestlcoin-cli
have monacoind    || dl "https://github.com/monacoinproject/monacoin/releases/download/v0.20.4/monacoin-0.20.4-$([ "$OS" = osx ] && echo osx64 || echo "${GH_SUF}").tar.gz" monacoind monacoin-cli
have vertcoind    || dl "https://github.com/vertcoin-project/vertcoin-core/releases/download/v23.2/vertcoin-23.2-${GH_SUF}.tar.gz" vertcoind vertcoin-cli
have dashd        || dl "https://github.com/dashpay/dash/releases/download/v23.1.8/dashcore-23.1.8-${GH_SUF}.tar.gz" dashd dash-cli
# litecoin: only x86_64 published for both OSes at 0.21.4
have litecoind    || dl "https://download.litecoin.org/litecoin-0.21.4/$([ "$OS" = osx ] && echo osx/litecoin-0.21.4-osx64 || echo linux/litecoin-0.21.4-x86_64-linux-gnu).tar.gz" litecoind litecoin-cli

rm -f "$WORK/dl.tar.gz"
log "done. daemons in $BIN:"; ls "$BIN" 2>/dev/null
