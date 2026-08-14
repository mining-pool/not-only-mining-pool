#!/usr/bin/env bash
# Reproducible end-to-end suite. Runs each coin's real-node test and prints a
# scoreboard. Daemons + redis + Go must be on PATH — the supported path is the
# Docker image (scripts/e2e/Dockerfile), which bundles everything:
#   docker build -t nomp-e2e -f scripts/e2e/Dockerfile .
#   docker run --rm nomp-e2e
# On a bare host, put the coin daemons on PATH first (see scripts/e2e/fetch-deps.sh).
#
# Usage: scripts/e2e/run-all.sh [coin ...]   (default: every coin)
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
HERE="$(dirname "${BASH_SOURCE[0]}")"

# --- GBT coins (bitcoind family) --------------------------------------------
# name sym daemon cli algo rpcport sport  extra-opts...
# RVN rides this path too: it is a getblocktemplate node, but the pool runs in
# kawpow engine mode (engine=kawpow) and the kawpow dialect miner drives it.
GBT_COINS=(
  "Bitcoin      BTC  bitcoind      bitcoin-cli     sha256d   18443 3032"
  "Litecoin     LTC  litecoind     litecoin-cli    scrypt    19443 3042  peers=2 gbtRules=mweb,segwit"
  "Dash         DASH dashd         dash-cli        x11       19543 3043  peers=2 gbtRules= sha256dBlock=0"
  "Groestlcoin  GRS  groestlcoind  groestlcoin-cli groestl   18444 3041  blockHasher=sha256 sha256dBlock=0 coinbaseHasher=sha256"
  "Monacoin     MONA monacoind     monacoin-cli    lyra2rev2 19643 3046  peers=2"
  "Vertcoin     VTC  vertcoind     vertcoin-cli    verthash  19743 3047  peers=2 waitReady=400"
  "Ravencoin    RVN  ravend        raven-cli       kawpow    19843 3048  peers=2 engine=kawpow waitReady=60"
)

# --- engine coins (dedicated runners; non-bitcoind node interaction) --------
# sym  script          rpcport sport
ENGINE_COINS=(
  "ETC  ethash.sh      8545  3045"
  "XMR  cryptonote.sh  18081 3040"
  "KAS  kaspa.sh       16110 3044"
)

declare -a RESULTS
record() { # <sym> <output>
  local sym="$1" out="$2"
  if   echo "$out" | grep -q "✅"; then
    echo "$out" | grep -E "✅|⏭"; RESULTS+=("$sym ✅ real block")
  elif echo "$out" | grep -q "⏭"; then
    echo "$out" | grep -E "⏭"; RESULTS+=("$sym ⏭ daemon not installed")
  else
    echo "$out"   # full output (node/pool diagnostics) so CI logs show the cause
    RESULTS+=("$sym ❌ see $WORK/$sym/pool.log")
  fi
}

want() { # <sym> — honour the coin filter
  [ ${#FILTER[@]} -eq 0 ] && return 0
  printf '%s\n' "${FILTER[@]}" | grep -qiw "$1"
}

FILTER=("$@")
for c in "${GBT_COINS[@]}"; do
  read -r _ sym _ <<<"$c"
  want "$sym" || continue
  echo "================ $sym ================"
  record "$sym" "$(bash "$HERE/gbt.sh" $c 2>&1)"
done

for e in "${ENGINE_COINS[@]}"; do
  read -r sym script rpcport sport <<<"$e"
  want "$sym" || continue
  echo "================ $sym ================"
  record "$sym" "$(bash "$HERE/$script" "$rpcport" "$sport" 2>&1)"
done

echo ""
echo "================ SCOREBOARD ================"
printf '  %s\n' "${RESULTS[@]}"

# Exit non-zero if any coin actually failed (❌). Skips (⏭, daemon not
# provisioned) are surfaced but do not fail the run unless E2E_STRICT=1, so a
# stale download URL or a missing per-arch binary doesn't wedge the whole suite.
failed=0
for r in "${RESULTS[@]}"; do
  case "$r" in
    *❌*) failed=1;;
    *⏭*) [ "${E2E_STRICT:-0}" = 1 ] && failed=1;;
  esac
done
exit $failed
