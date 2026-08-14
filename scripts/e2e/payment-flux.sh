#!/usr/bin/env bash
# End-to-end test that a Zcash-family ENGINE coin (Flux / ZelHash, Equihash 125,4)
# pays out through the shared payout processor. Like payment-rvn.sh it exercises
# the engine-mode integration: NewEnginePool constructing + serving the
# PaymentManager, the equihash engine starting against a LIVE fluxd (zcash-style
# getblocktemplate with coinbasetxn), and the payer validating a real fluxd wallet
# before classifyBlock/attribute/sendmany run against real wallet RPCs.
#
# fluxd needs ~775MB of zk params, so this runs only in the isolated e2e-flux CI
# job (Dockerfile.flux), never the main suite. kawpow/RVNPAY already proves the
# identical payout code path; this additionally proves the equihash engine drives
# a live node. It skips cleanly when fluxd is absent.
#
# Usage: payment-flux.sh [rpcport] [stratumport]
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

SYM=FLUXPAY
DAEMON=fluxd CLI=flux-cli
RPCPORT="${1:-16144}" SPORT="${2:-3051}"
COIN=Flux   # must match config coin.name (the redis key prefix)

have "$DAEMON" || { skip "$SYM: $DAEMON not found on PATH"; exit 2; }
ensure_redis || exit 1

DIR="$WORK/$SYM"; DIR2="$WORK/${SYM}b"; rm -rf "$DIR" "$DIR2"; mkdir -p "$DIR" "$DIR2"
P1=$((RPCPORT+1000)); P2=$((RPCPORT+1001))
cli()  { "$CLI" -regtest -datadir="$DIR" -rpcport=$RPCPORT -rpcuser=u -rpcpassword=p "$@"; }
w()    { cli "$@"; }   # fluxd uses a single default wallet
trap 'cli stop >/dev/null 2>&1; pkill -9 -f "$DIR/pool" 2>/dev/null; "$CLI" -datadir="$DIR2" -rpcport=$P2 -rpcuser=u -rpcpassword=p stop >/dev/null 2>&1; cleanup' EXIT

mkconf() { cat > "$1/flux.conf" <<EOF
regtest=1
server=1
rpcuser=u
rpcpassword=p
rpcport=$2
port=$3
rpcallowip=127.0.0.1
showmetrics=0
EOF
}
rpcflags() { echo "-rpcport=$1 -port=$2 -rpcuser=u -rpcpassword=p -rpcallowip=127.0.0.1"; }

log "$SYM: starting fluxd regtest (2 nodes for GBT)"
mkconf "$DIR" $RPCPORT $P1
"$DAEMON" -datadir="$DIR" -conf="$DIR/flux.conf" $(rpcflags $RPCPORT $P1) -listen -daemon >"$DIR/node.log" 2>&1
mkconf "$DIR2" $P2 $((P1+1))
"$DAEMON" -datadir="$DIR2" -conf="$DIR2/flux.conf" $(rpcflags $P2 $((P1+1))) -connect=127.0.0.1:$P1 -daemon >"$DIR2/node.log" 2>&1
for i in $(seq 1 150); do cli getblockcount >/dev/null 2>&1 && break; sleep 1; done
if ! cli getblockcount >/dev/null 2>&1; then
  fail "$SYM: node did not come up"
  echo "--- flux-cli getblockcount (unsuppressed) ---" >&2; cli getblockcount >&2 2>&1 || true
  echo "--- node.log ---" >&2; tail -15 "$DIR/node.log" 2>/dev/null >&2
  echo "--- debug.log (rpc/http/bind/error) ---" >&2; grep -iE "http|rpc|bind|error|warmup|init message" "$DIR"/regtest/debug.log 2>/dev/null | tail -25 >&2
  exit 1
fi
sleep 3

POOL_ADDR=$(w getnewaddress 2>/dev/null)
MINER_ADDR=$(w getnewaddress 2>/dev/null)
[ -z "$POOL_ADDR" ] || [ -z "$MINER_ADDR" ] && { fail "$SYM: could not get wallet addresses"; exit 1; }

# Pre-fund with mature coins, then one more block whose coinbase stands in for a
# pool-found block. Prefer generatetoaddress (coinbase → POOL_ADDR); fall back to
# the zcash-classic `generate`.
gen() { # <n> <addr>
  w generatetoaddress "$1" "$2" >/dev/null 2>&1 || w generate "$1" >/dev/null 2>&1
}
gen 120 "$POOL_ADDR"
H=$(cli getblockcount)
BLOCKHASH=$(cli getblockhash "$H")
TXID=$(cli getblock "$BLOCKHASH" | python3 -c "import sys,json;print(json.load(sys.stdin)['tx'][0])")
[ -z "$TXID" ] && { fail "$SYM: could not resolve coinbase txid"; exit 1; }
log "$SYM: node up height=$H pool=$POOL_ADDR miner=$MINER_ADDR block=$H tx=$TXID balance=$(w getbalance 2>/dev/null)"

log "$SYM: building equihash pool (default build)"
build_pool "$DIR/pool" || { fail "$SYM: pool build failed"; exit 1; }

python3 - "$POOL_ADDR" "$RPCPORT" "$SPORT" "$DIR" "$COIN" <<'PY'
import json,sys
addr,rpc,sport,d,coin=sys.argv[1:6]
pay={"interval":4,"minPayment":0,"daemon":0,"payMode":"prop","magnitude":1e8,
     "minConfirmations":1,"addressCheckMethod":"validateaddress",
     "sendManyDummy":"","omitSendManyDummy":False}
c={"coin":{"name":coin,"symbol":"FLUX"},"algorithm":{"name":"zelhash","multiplier":0},
 "engine":"zelhash","disablePayment":False,"payment":pay,
 "poolAddress":{"address":addr,"type":"p2pkh"},"rewardRecipients":[],
 "blockRefreshInterval":500,"jobRebroadcastTimeout":55,"connectionTimeout":600,
 "banning":{"time":600,"invalidPercent":50,"checkThreshold":500,"purgeInterval":300},
 "ports":{sport:{"diff":0.001,"varDiff":None,"tls":None}},
 "daemons":[{"host":"127.0.0.1","port":int(rpc),"user":"u","password":"p"}],
 "p2p":None,"api":{"host":"0.0.0.0","port":0},
 "storage":{"network":"tcp","host":"127.0.0.1","port":6379,"tls":None}}
json.dump(c,open(d+"/config.json","w"),indent=2)
PY

# Inject the pending-block record the pool would have written on a solve, plus the
# round contribution the prop scheme attributes it to.
redis-cli keys "$COIN:*" 2>/dev/null | xargs -r redis-cli del >/dev/null 2>&1
redis-cli sadd "$COIN:blocks:pending" "$BLOCKHASH:$TXID:$H:$MINER_ADDR:1" >/dev/null
redis-cli hset "$COIN:shares:round$H" "$MINER_ADDR" 1 >/dev/null

free_port "$SPORT"
( cd "$DIR" && "$DIR/pool" -c config.json -l info >"$DIR/pool.log" 2>&1 & )
wait_pool_started "$DIR/pool.log" || { fail "$SYM: equihash pool did not start against fluxd"; grep -iE "engine|gbt|template|fatal|error" "$DIR/pool.log" | tail -15; exit 1; }
grep -q "payments:" "$DIR/pool.log" || { fail "$SYM: payment processor did not init on the engine pool"; grep -iE "payment|fatal|error" "$DIR/pool.log" | tail -10; exit 1; }
log "$SYM: equihash pool up against live fluxd, payer initialized"

paid=""
for i in $(seq 1 30); do
  paid=$(redis-cli hget "$COIN:payouts" "$MINER_ADDR" 2>/dev/null)
  [ -n "$paid" ] && python3 -c "import sys;sys.exit(0 if float('$paid')>0 else 1)" 2>/dev/null && break
  sleep 2
done
pkill -9 -f "$DIR/pool" 2>/dev/null

if [ -n "$paid" ] && python3 -c "import sys;sys.exit(0 if float('$paid')>0 else 1)" 2>/dev/null; then
  ok "$SYM (zelhash): engine pool paid the miner via the shared processor — payout=$paid"
  exit 0
fi
fail "$SYM (zelhash): engine pool did not pay the miner"
grep -iE "payment|sendmany|insufficient|fatal|error|orphan|coinbase" "$DIR/pool.log" | tail -20
exit 1
