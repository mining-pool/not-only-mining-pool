#!/usr/bin/env bash
# End-to-end test that a bitcoin-family ENGINE coin (Ravencoin / kawpow) pays out
# through the shared payout processor — via REAL mining, not an injected block.
# It drives the full path the pool owns: the kawpow engine solves a block, submits
# it to ravend, resolves the coinbase txid (CheckBlockAccepted), PutShare records
# it as a pending block, and the payer attributes + sendmany-pays the finder.
#
# One node reused; the e2ervnminer (kawpow dialect) mines against the pool.
#
# Usage: payment-rvn.sh [rpcport] [stratumport]
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

SYM=RVNPAY
DAEMON=ravend CLI=raven-cli
RPCPORT="${1:-19845}" SPORT="${2:-3049}"
COIN=Ravencoin

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
POOL_ADDR=$(w getnewaddress "" legacy 2>/dev/null || w getnewaddress)
MINER_ADDR=$(w getnewaddress "" legacy 2>/dev/null || w getnewaddress)
[ -z "$POOL_ADDR" ] || [ -z "$MINER_ADDR" ] && { fail "$SYM: could not get wallet addresses"; exit 1; }

# Pre-fund so the wallet has mature coins for sendmany (payouts don't spend the
# freshly-mined coinbase). Then wait for wall-clock to pass the tip block time so
# the pool's newly-built block isn't rejected time-too-old.
w generatetoaddress 120 "$POOL_ADDR" >/dev/null 2>&1
tip_time() { cli getblock "$(cli getbestblockhash)" 2>/dev/null | python3 -c "import sys,json;print(json.load(sys.stdin).get('time',0))" 2>/dev/null || echo 0; }
for i in $(seq 1 150); do [ "$(date +%s)" -gt "$(( $(tip_time) + 1 ))" ] && break; sleep 1; done
log "$SYM: node up height=$(cli getblockcount) pool=$POOL_ADDR miner=$MINER_ADDR"

# Diagnostic: the node's own tip header size reveals whether KAWPOW is active
# (80-byte legacy header vs 120-byte kawpow header with nHeight+nNonce64+mixhash).
HH=$(cli getblockheader "$(cli getbestblockhash)" false 2>/dev/null)
echo "$SYM DIAG: tip header = $(( ${#HH} / 2 )) bytes; blockversion=$(cli getblock "$(cli getbestblockhash)" 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin).get("version"))' 2>/dev/null)" >&2
NODEBLK=$(cli getblock "$(cli getbestblockhash)" 0 2>/dev/null)
echo "$SYM DIAG: node block hex head = ${NODEBLK:0:260}" >&2

log "$SYM: building kawpow pool + e2ervnminer (-tags kawpow)"
build_pool "$DIR/pool" kawpow || { fail "$SYM: pool build failed"; exit 1; }
build_tool e2ervnminer "$DIR/miner" kawpow || { fail "$SYM: miner build failed"; exit 1; }

# Payment-enabled kawpow config, prop mode. The port diff is tiny so the CPU
# miner finds a share fast; on regtest that share also clears the network target,
# so the engine submits it as a real block.
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

redis-cli keys "$COIN:*" 2>/dev/null | xargs -r redis-cli del >/dev/null 2>&1
free_port "$SPORT"
( cd "$DIR" && "$DIR/pool" -c config.json -l info >"$DIR/pool.log" 2>&1 & )
wait_pool_started "$DIR/pool.log" || { fail "$SYM: pool did not start"; tail -30 "$DIR/pool.log"; exit 1; }

# Mine until the pool's block is ACCEPTED by the node (chain grows). The miner is
# one-shot per run, so loop; the pool records the pending block itself on accept.
H0=$(cli getblockcount)
LANDED=0
for attempt in $(seq 1 40); do
  with_timeout 30 "$DIR/miner" -pool 127.0.0.1:$SPORT -worker "$MINER_ADDR" >>"$DIR/miner.log" 2>&1
  sleep 1
  if [ "$(cli getblockcount)" -gt "$H0" ]; then LANDED=1; break; fi
done

if [ "$LANDED" != 1 ]; then
  fail "$SYM: pool never landed an accepted block after 40 attempts"
  echo "--- pool.log (block/reject) ---" >&2
  grep -iE "block candidate|rejected the block|found block|high-hash|bad-|time-too|stale|duplicate|error with daemon|failed submitting" "$DIR/pool.log" | tail -10 >&2
  echo "--- ravend debug.log (block validity) ---" >&2
  grep -iE "ERROR|reject|CheckBlock|ConnectBlock|proof of work|high-hash|bad-|merkle|AcceptBlock|InvalidChain|Misbehaving" "$DIR"/regtest/debug.log 2>/dev/null | tail -20 >&2
  echo "--- one manual submitblock (unsuppressed) ---" >&2
  cand=$(grep -oiE "block candidate at height [0-9]+ hash [0-9a-f]+" "$DIR/pool.log" | tail -1)
  echo "last candidate: $cand" >&2
  exit 1
fi
log "$SYM: pool mined + node accepted a block (height $H0 -> $(cli getblockcount))"

# Confirm the block so the payer (minConfirmations=1) will distribute it.
w generatetoaddress 1 "$POOL_ADDR" >/dev/null 2>&1

paid=""
for i in $(seq 1 30); do
  paid=$(redis-cli hget "$COIN:payouts" "$MINER_ADDR" 2>/dev/null)
  [ -n "$paid" ] && python3 -c "import sys;sys.exit(0 if float('$paid')>0 else 1)" 2>/dev/null && break
  sleep 2
done
pkill -9 -f "$DIR/pool" 2>/dev/null

if [ -n "$paid" ] && python3 -c "import sys;sys.exit(0 if float('$paid')>0 else 1)" 2>/dev/null; then
  received=$(w getreceivedbyaddress "$MINER_ADDR" 0 2>/dev/null)
  ok "$SYM (kawpow): mined a real block → pool recorded it → payer paid the finder — payout=$paid received=$received"
  exit 0
fi
fail "$SYM (kawpow): block landed but the miner was not paid"
echo "--- pending / payouts ---" >&2
redis-cli smembers "$COIN:blocks:pending" >&2; redis-cli hgetall "$COIN:payouts" >&2
grep -iE "payment|sendmany|insufficient|orphan|gettransaction|coinbase" "$DIR/pool.log" | tail -20 >&2
exit 1
