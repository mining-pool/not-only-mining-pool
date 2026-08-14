#!/usr/bin/env bash
# Reproducible end-to-end test for the PAYOUT processor against a real bitcoind
# regtest wallet. Runs a payment-enabled pool once per payMode (prop, pplns,
# solo, pps): mine a block to the pool address, mature the coinbase, and assert
# the pool actually pays the miner via sendmany (redis payouts). One node is
# reused across modes; redis is cleared between them.
#
# Usage: payment.sh [rpcport] [stratumport]
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

SYM=PAY
DAEMON=bitcoind CLI=bitcoin-cli
RPCPORT="${1:-18455}" SPORT="${2:-3050}"
COIN=Bitcoin

have "$DAEMON" || { skip "$SYM: $DAEMON not found on PATH"; exit 2; }
ensure_redis || exit 1

DIR="$WORK/$SYM"; rm -rf "$DIR"; mkdir -p "$DIR"
P1=$((RPCPORT+1000))
cli() { "$CLI" -datadir="$DIR" -rpcport=$RPCPORT -rpcuser=u -rpcpassword=p "$@"; }
w()   { cli -rpcwallet=pool "$@"; }
trap 'cli stop >/dev/null 2>&1; pkill -9 -f "$DIR/pool" 2>/dev/null; cleanup' EXIT

cat > "$DIR/node.conf" <<EOF
regtest=1
server=1
fallbackfee=0.0002
[regtest]
rpcuser=u
rpcpassword=p
rpcport=$RPCPORT
port=$P1
rpcallowip=127.0.0.1
EOF

log "$SYM: starting bitcoind regtest"
"$DAEMON" -datadir="$DIR" -conf="$DIR/node.conf" -rpcport=$RPCPORT -port=$P1 -rpcuser=u -rpcpassword=p -rpcallowip=127.0.0.1 -daemon >"$DIR/node.log" 2>&1
for i in $(seq 1 30); do cli getblockcount >/dev/null 2>&1 && break; sleep 1; done
cli getblockcount >/dev/null 2>&1 || { fail "$SYM: node did not come up"; tail -8 "$DIR"/regtest/debug.log 2>/dev/null; exit 1; }

# single loaded wallet so the pool's wallet RPCs (gettransaction/sendmany) route to it
cli createwallet pool >/dev/null 2>&1 || cli loadwallet pool >/dev/null 2>&1 || true
POOL_ADDR=$(w getnewaddress "" legacy)
MINER_ADDR=$(w getnewaddress "" legacy)   # the miner's worker name == its payout address
JUNK_ADDR=$(w getnewaddress "" legacy)
[ -z "$POOL_ADDR" ] || [ -z "$MINER_ADDR" ] && { fail "$SYM: could not get wallet addresses"; exit 1; }

# pre-fund the wallet with mature coins so sendmany can pay from them (payouts do
# not spend the freshly-mined coinbase, which stays immature), then wait for
# wall-clock to catch up to the chain time these rapid blocks pushed ahead.
w generatetoaddress 101 "$POOL_ADDR" >/dev/null 2>&1
wait_chain_time() {
  local n="${1:-30}" i ct
  for i in $(seq 1 "$n"); do
    ct=$(cli getblocktemplate '{"rules":["segwit"]}' 2>/dev/null | python3 -c "import sys,json;print(json.load(sys.stdin).get('curtime',0))" 2>/dev/null || echo 0)
    [ "${ct:-0}" -le "$(( $(date +%s) + 3 ))" ] && return 0; sleep 1
  done
}
wait_chain_time 150
log "$SYM: node up height=$(cli getblockcount) pool=$POOL_ADDR miner=$MINER_ADDR"

log "$SYM: building pool + e2eminer"
build_pool "$DIR/pool" || { fail "$SYM: pool build failed"; exit 1; }
build_tool e2eminer "$DIR/miner" || { fail "$SYM: miner build failed"; exit 1; }

# mkconfig <mode> — payment-enabled config for one payMode.
mkconfig() {
  python3 - "$POOL_ADDR" "$RPCPORT" "$SPORT" "$DIR" "$COIN" "$1" <<'PY'
import json,sys
addr,rpc,sport,d,coin,mode=sys.argv[1:7]
# minConfirmations 1: the payer distributes a block once it has 1 confirmation
# (the maturity policy itself is unit-tested); sendmany still spends the mature
# pre-funded balance, so we avoid generating 100 blocks (which would drift the
# chain time past the pool's nTime window between modes).
pay={"interval":4,"minPayment":0,"daemon":0,"payMode":mode,"pplnsWindow":0,"ppsRate":0,
     "magnitude":1e8,"minConfirmations":1,"addressCheckMethod":"getaddressinfo",
     "sendManyDummy":"","omitSendManyDummy":False}
if mode=="pps": pay["ppsRate"]=100   # coin per share-difficulty; block reward funds the wallet
c={"coin":{"name":coin,"symbol":"BTC"},"algorithm":{"name":"sha256d","multiplier":0,"sha256dBlockHasher":True},
 "disablePayment":False,"payment":pay,
 "poolAddress":{"address":addr,"type":"p2pkh"},"rewardRecipients":[],
 "blockRefreshInterval":500,"jobRebroadcastTimeout":55,"connectionTimeout":600,
 "banning":{"time":600,"invalidPercent":50,"checkThreshold":500,"purgeInterval":300},
 "ports":{sport:{"diff":0.0001,"varDiff":{"minDiff":0.00001,"maxDiff":1000,"targetTime":15,"retargetTime":90,"variancePercent":30},"tls":None}},
 "daemons":[{"host":"127.0.0.1","port":int(rpc),"user":"u","password":"p"}],
 "p2p":None,"api":{"host":"0.0.0.0","port":0},
 "storage":{"network":"tcp","host":"127.0.0.1","port":6379,"tls":None}}
json.dump(c,open(d+"/config.json","w"),indent=2)
PY
}

paid_gt0() { [ -n "$1" ] && python3 -c "import sys;sys.exit(0 if float('$1')>0 else 1)" 2>/dev/null; }

# run_mode <mode> — start pool, mine, mature, poll payouts. Echoes the paid
# amount on stdout; returns 0 iff the miner was paid. Diagnostics go to stderr.
run_mode() {
  local mode="$1"
  redis-cli keys "$COIN:*" 2>/dev/null | xargs -r redis-cli del >/dev/null 2>&1
  mkconfig "$mode"
  free_port "$SPORT"
  ( cd "$DIR" && "$DIR/pool" -c config.json -l info >"pool.$mode.log" 2>&1 & )
  wait_pool_started "$DIR/pool.$mode.log" || { tail -20 "$DIR/pool.$mode.log" >&2; return 1; }
  wait_chain_time 30   # small per-mode drift from the previous confirm block

  local H0; H0=$(cli getblockcount)
  with_timeout 240 "$DIR/miner" -pool 127.0.0.1:$SPORT -worker "$MINER_ADDR" -algo sha256d -coinbasehash sha256d -rpc "http://u:p@127.0.0.1:$RPCPORT" >"$DIR/miner.$mode.log" 2>&1
  sleep 2
  [ "$(cli getblockcount)" -gt "${H0:-0}" ] || { echo "no block mined" >&2; pkill -9 -f "$DIR/pool" 2>/dev/null; return 1; }
  w generatetoaddress 1 "$JUNK_ADDR" >/dev/null 2>&1   # 1 confirmation (minConfirmations=1)

  local paid=""
  for i in $(seq 1 20); do
    paid=$(redis-cli hget "$COIN:payouts" "$MINER_ADDR" 2>/dev/null)
    paid_gt0 "$paid" && break
    sleep 2
  done
  pkill -9 -f "$DIR/pool" 2>/dev/null; sleep 1
  echo "$paid"
  paid_gt0 "$paid"
}

declare -a RESULTS
FAILED=0
for mode in prop pplns solo pps; do
  log "$SYM: payMode=$mode"
  paid=$(run_mode "$mode")
  if paid_gt0 "$paid"; then
    RESULTS+=("$mode=$paid")
  else
    RESULTS+=("$mode=FAIL"); FAILED=1
    grep -iE "payment|sendmany|paid|payout|maturit|insufficient|fatal" "$DIR/pool.$mode.log" 2>/dev/null | tail -6
  fi
done

if [ "$FAILED" = 0 ]; then
  ok "$SYM: every payMode paid the miner — ${RESULTS[*]}"
  exit 0
else
  fail "$SYM: a payMode did not pay — ${RESULTS[*]}"
  exit 1
fi
