#!/usr/bin/env bash
# End-to-end test that a bitcoin-family ENGINE coin (Ravencoin / kawpow) pays out
# through the shared payout processor, for every payMode. It exercises the
# engine-mode integration the GBT payment.sh cannot: NewEnginePool constructing +
# serving the PaymentManager, and PaymentManager.Init validating a real ravend
# wallet (validateaddress ownership) before classifyBlock/attribute/sendmany run
# against real wallet RPCs — across prop, pplns, solo and pps.
#
# Why inject the block instead of mining it: Ravencoin REGTEST does not run KAWPOW
# — its nodes produce/accept only the legacy 80-byte header, so the pool's real
# (mainnet-format, 120-byte) kawpow block is rejected "Block decode failed (-22)"
# and can never land. Real kawpow mining → accepted block is therefore impossible
# on regtest (verified: -kawpowactivationtime has no effect, tip header stays 80
# bytes). So we generate the block on the node and inject the pending-block record
# the pool would have written on a solve. The block-recording path itself
# (OnSubmit → CheckBlockAccepted → PutShare) is covered by unit tests and the
# shared CheckBlockAccepted (real-tested by the GBT PAY leg); everything downstream
# — the engine pool serving the payer and the real ravend sendmany — runs for real.
#
# Usage: payment-rvn.sh [rpcport] [stratumport]
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

SYM=RVNPAY
DAEMON=ravend CLI=raven-cli
RPCPORT="${1:-19845}" SPORT="${2:-3049}"
COIN=Ravencoin   # must match config coin.name (the redis key prefix)

have "$DAEMON" || { skip "$SYM: $DAEMON not found on PATH"; exit 2; }
ensure_redis || exit 1

DIR="$WORK/$SYM"; DIR2="$WORK/${SYM}b"; rm -rf "$DIR" "$DIR2"; mkdir -p "$DIR" "$DIR2"
P1=$((RPCPORT+1000)); P2=$((RPCPORT+1001))
cli()  { "$CLI" -datadir="$DIR" -rpcport=$RPCPORT -rpcuser=u -rpcpassword=p "$@"; }
w()    { cli -rpcwallet=pool "$@" 2>/dev/null || cli "$@"; }
trap 'cli stop >/dev/null 2>&1; pkill -9 -f "$DIR/pool" 2>/dev/null; "$CLI" -datadir="$DIR2" -rpcport=$P2 -rpcuser=u -rpcpassword=p stop >/dev/null 2>&1; cleanup' EXIT

mkconf() { cat > "$1/node.conf" <<EOF
regtest=1
server=1
fallbackfee=0.0002
[regtest]
rpcuser=u
rpcpassword=p
rpcport=$2
port=$3
rpcallowip=127.0.0.1
EOF
}
rpcflags() { echo "-rpcport=$1 -port=$2 -rpcuser=u -rpcpassword=p -rpcallowip=127.0.0.1"; }

log "$SYM: starting ravend regtest (2 nodes for GBT)"
mkconf "$DIR" $RPCPORT $P1
"$DAEMON" -datadir="$DIR" -conf="$DIR/node.conf" $(rpcflags $RPCPORT $P1) -listen -daemon >"$DIR/node.log" 2>&1
mkconf "$DIR2" $P2 $((P1+1))
"$DAEMON" -datadir="$DIR2" -conf="$DIR2/node.conf" $(rpcflags $P2 $((P1+1))) -connect=127.0.0.1:$P1 -daemon >"$DIR2/node.log" 2>&1
for i in $(seq 1 60); do cli getblockcount >/dev/null 2>&1 && break; sleep 1; done
cli getblockcount >/dev/null 2>&1 || { fail "$SYM: node did not come up"; tail -20 "$DIR"/regtest/debug.log 2>/dev/null; exit 1; }
sleep 3

cli createwallet pool >/dev/null 2>&1 || cli loadwallet pool >/dev/null 2>&1 || true
POOL_ADDR=$(w getnewaddress 2>/dev/null)
MINER_ADDR=$(w getnewaddress 2>/dev/null)   # worker name == payout address
[ -z "$POOL_ADDR" ] || [ -z "$MINER_ADDR" ] && { fail "$SYM: could not get wallet addresses"; exit 1; }

# Pre-fund with mature coins so sendmany can pay from them, then generate one more
# block whose coinbase (to POOL_ADDR) stands in for a pool-found block, reused by
# every mode (each clears redis and re-injects its own state).
w generatetoaddress 120 "$POOL_ADDR" >/dev/null 2>&1
H=$(cli getblockcount)
BLOCKHASH=$(cli getblockhash "$H")
TXID=$(cli getblock "$BLOCKHASH" | python3 -c "import sys,json;print(json.load(sys.stdin)['tx'][0])")
[ -z "$TXID" ] && { fail "$SYM: could not resolve coinbase txid"; exit 1; }
log "$SYM: node up height=$H pool=$POOL_ADDR miner=$MINER_ADDR block=$H tx=$TXID"

log "$SYM: building kawpow pool (-tags kawpow)"
build_pool "$DIR/pool" kawpow || { fail "$SYM: pool build failed"; exit 1; }

# mkconfig <mode> — payment-enabled kawpow engine config. Ravencoin is a
# Bitcoin-0.16 fork: ownership check is validateaddress and sendmany keeps the
# leading dummy arg.
mkconfig() {
  python3 - "$POOL_ADDR" "$RPCPORT" "$SPORT" "$DIR" "$COIN" "$1" <<'PY'
import json,sys
addr,rpc,sport,d,coin,mode=sys.argv[1:7]
pay={"interval":4,"minPayment":0,"daemon":0,"payMode":mode,"pplnsWindow":0,"ppsRate":0,
     "magnitude":1e8,"minConfirmations":1,"addressCheckMethod":"validateaddress",
     "sendManyDummy":"","omitSendManyDummy":False}
if mode=="pplns": pay["pplnsWindow"]=100
if mode=="pps":   pay["ppsRate"]=1
c={"coin":{"name":coin,"symbol":"RVN","gbtRules":["segwit"]},
 "algorithm":{"name":"kawpow","multiplier":0,"sha256dBlockHasher":True},
 "engine":"kawpow","disablePayment":False,"payment":pay,
 "poolAddress":{"address":addr,"type":"p2pkh"},"rewardRecipients":[],
 "blockRefreshInterval":500,"jobRebroadcastTimeout":55,"connectionTimeout":600,
 "banning":{"time":600,"invalidPercent":50,"checkThreshold":500,"purgeInterval":300},
 "ports":{sport:{"diff":0.00000001,"varDiff":None,"tls":None}},
 "daemons":[{"host":"127.0.0.1","port":int(rpc),"user":"u","password":"p"}],
 "p2p":None,"api":{"host":"0.0.0.0","port":0},
 "storage":{"network":"tcp","host":"127.0.0.1","port":6379,"tls":None}}
json.dump(c,open(d+"/config.json","w"),indent=2)
PY
}

# seed_mode <mode> — inject the pending block plus the share state each payMode
# attributes the reward to (a single miner, so every scheme pays it in full).
seed_mode() {
  local mode="$1"
  redis-cli keys "$COIN:*" 2>/dev/null | xargs -r redis-cli del >/dev/null 2>&1
  redis-cli sadd "$COIN:blocks:pending" "$BLOCKHASH:$TXID:$H:$MINER_ADDR:1" >/dev/null
  case "$mode" in
    prop)  redis-cli hset "$COIN:shares:round$H" "$MINER_ADDR" 1 >/dev/null;;
    solo)  : ;; # finder (in the pending record) takes the whole reward
    pplns) redis-cli zadd "$COIN:shares:pplnslog" 1 "$MINER_ADDR:1:1" >/dev/null;;
    pps)   redis-cli zadd "$COIN:shares:pplnslog" 1 "$MINER_ADDR:1:1" >/dev/null;;
  esac
}

# run_mode <mode> — start the engine pool, poll payouts. Echoes the paid amount.
run_mode() {
  local mode="$1"
  seed_mode "$mode"
  mkconfig "$mode"
  free_port "$SPORT"
  ( cd "$DIR" && "$DIR/pool" -c config.json -l info >"$DIR/pool.$mode.log" 2>&1 & )
  wait_pool_started "$DIR/pool.$mode.log" || { echo "pool did not start" >&2; tail -20 "$DIR/pool.$mode.log" >&2; return 1; }
  grep -q "payments:" "$DIR/pool.$mode.log" || { echo "payer did not init" >&2; return 1; }

  local paid=""
  for i in $(seq 1 20); do
    paid=$(redis-cli hget "$COIN:payouts" "$MINER_ADDR" 2>/dev/null)
    [ -n "$paid" ] && python3 -c "import sys;sys.exit(0 if float('$paid')>0 else 1)" 2>/dev/null && break
    sleep 2
  done
  pkill -9 -f "$DIR/pool" 2>/dev/null; sleep 1
  echo "$paid"
}

RESULTS=""; FAILED=0
for mode in prop pplns solo pps; do
  log "$SYM: payMode=$mode"
  paid=$(run_mode "$mode")
  if [ -n "$paid" ] && python3 -c "import sys;sys.exit(0 if float('$paid')>0 else 1)" 2>/dev/null; then
    RESULTS="$RESULTS $mode=$paid"
  else
    RESULTS="$RESULTS $mode=FAIL"; FAILED=1
    grep -iE "payment|sendmany|insufficient|fatal|error|orphan" "$DIR/pool.$mode.log" 2>/dev/null | tail -8 >&2
  fi
done

if [ "$FAILED" = 0 ]; then
  ok "$SYM (kawpow): engine pool paid every payMode via the shared processor —$RESULTS"
  exit 0
fi
fail "$SYM (kawpow): a payMode did not pay —$RESULTS"
exit 1
