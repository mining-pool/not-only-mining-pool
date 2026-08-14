#!/usr/bin/env bash
# End-to-end test that a bitcoin-family ENGINE coin (Ravencoin / kawpow) pays out
# through the shared payout processor. It exercises the engine-mode integration
# that the GBT payment.sh cannot: NewEnginePool constructing + serving the
# PaymentManager, and PaymentManager.Init validating a real ravend wallet
# (validateaddress ownership) before classifyBlock/attribute/sendmany run against
# real wallet RPCs.
#
# kawpow is CPU-heavy and does not reliably LAND a regtest block (see gbt.sh), so
# rather than depend on a lucky solve we generate the block on the node and inject
# the pending-block record the pool would have written (kawpow's own txid
# resolution is covered by unit tests + the shared CheckBlockAccepted). The payout
# half — the part unique to the engine pool — runs for real.
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
# block whose coinbase (to POOL_ADDR) stands in for a pool-found block.
w generatetoaddress 120 "$POOL_ADDR" >/dev/null 2>&1
H=$(cli getblockcount)
BLOCKHASH=$(cli getblockhash "$H")
TXID=$(cli getblock "$BLOCKHASH" | python3 -c "import sys,json;print(json.load(sys.stdin)['tx'][0])")
[ -z "$TXID" ] && { fail "$SYM: could not resolve coinbase txid"; exit 1; }
log "$SYM: node up height=$H pool=$POOL_ADDR miner=$MINER_ADDR block=$H tx=$TXID"

log "$SYM: building kawpow pool (-tags kawpow)"
build_pool "$DIR/pool" kawpow || { fail "$SYM: pool build failed"; exit 1; }

# Payment-enabled kawpow engine config. Ravencoin is a Bitcoin-0.16 fork, so its
# ownership check is validateaddress (no getaddressinfo) and sendmany keeps the
# leading dummy arg.
python3 - "$POOL_ADDR" "$RPCPORT" "$SPORT" "$DIR" "$COIN" <<'PY'
import json,sys
addr,rpc,sport,d,coin=sys.argv[1:6]
pay={"interval":4,"minPayment":0,"daemon":0,"payMode":"prop","magnitude":1e8,
     "minConfirmations":1,"addressCheckMethod":"validateaddress",
     "sendManyDummy":"","omitSendManyDummy":False}
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

# Inject the pending-block record the pool would have written on a kawpow solve,
# plus the round contribution the payer attributes it to.
redis-cli keys "$COIN:*" 2>/dev/null | xargs -r redis-cli del >/dev/null 2>&1
redis-cli sadd "$COIN:blocks:pending" "$BLOCKHASH:$TXID:$H:$MINER_ADDR:1" >/dev/null
redis-cli hset "$COIN:shares:round$H" "$MINER_ADDR" 1 >/dev/null

free_port "$SPORT"
( cd "$DIR" && "$DIR/pool" -c config.json -l info >"$DIR/pool.log" 2>&1 & )
wait_pool_started "$DIR/pool.log" || { fail "$SYM: pool did not start"; tail -30 "$DIR/pool.log"; exit 1; }
grep -q "payments:" "$DIR/pool.log" || { fail "$SYM: payment processor did not init on the engine pool"; grep -iE "payment|fatal|error" "$DIR/pool.log" | tail -10; exit 1; }
log "$SYM: engine pool up with payments"

paid=""
for i in $(seq 1 30); do
  paid=$(redis-cli hget "$COIN:payouts" "$MINER_ADDR" 2>/dev/null)
  [ -n "$paid" ] && python3 -c "import sys;sys.exit(0 if float('$paid')>0 else 1)" 2>/dev/null && break
  sleep 2
done

pkill -9 -f "$DIR/pool" 2>/dev/null
if [ -n "$paid" ] && python3 -c "import sys;sys.exit(0 if float('$paid')>0 else 1)" 2>/dev/null; then
  received=$(w getreceivedbyaddress "$MINER_ADDR" 0 2>/dev/null)
  ok "$SYM (kawpow): engine pool paid the miner via the shared processor — payout=$paid received=$received"
  exit 0
fi
fail "$SYM (kawpow): engine pool did not pay the miner"
grep -iE "payment|sendmany|insufficient|fatal|error|orphan" "$DIR/pool.log" | tail -15
exit 1
