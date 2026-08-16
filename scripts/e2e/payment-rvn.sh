#!/usr/bin/env bash
# End-to-end test that a bitcoin-family ENGINE coin (Ravencoin / kawpow) pays out
# through the shared payout processor — via REAL mining. It drives the full path
# the pool owns and inject would hide: the kawpow engine solves a block, submits
# it to ravend, resolves the coinbase txid (CheckBlockAccepted), PutShare records
# it as a pending block, then the payer attributes + sendmany-pays the finder.
#
# Ravencoin regtest hard-codes the KAWPOW activation TIME to 3582830167 (year
# 2083) — before it, nodes only accept the legacy 80-byte header, so a real kawpow
# block is rejected "Block decode failed". There is no CLI knob, but activation is
# by block time, so setmocktime past 2083 turns KAWPOW on without recompiling: we
# pre-fund with legacy blocks (mature coins for sendmany), then jump the clock so
# the pool's mined block is a real, node-accepted KAWPOW block.
#
# Usage: payment-rvn.sh [rpcport] [stratumport]
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

SYM=RVNPAY
DAEMON=ravend CLI=raven-cli
RPCPORT="${1:-19845}" SPORT="${2:-3049}"
COIN=Ravencoin
KAWPOW_TIME=3600000000   # > regtest nKAWPOWActivationTime (3582830167), < uint32 max

have "$DAEMON" || { skip "$SYM: $DAEMON not found on PATH"; exit 2; }
ensure_redis || exit 1

DIR="$WORK/$SYM"; DIR2="$WORK/${SYM}b"; rm -rf "$DIR" "$DIR2"; mkdir -p "$DIR" "$DIR2"
P1=$((RPCPORT+1000)); P2=$((RPCPORT+1001))
cli()  { "$CLI" -datadir="$DIR" -rpcport=$RPCPORT -rpcuser=u -rpcpassword=p "$@"; }
cli2() { "$CLI" -datadir="$DIR2" -rpcport=$P2 -rpcuser=u -rpcpassword=p "$@"; }
w()    { cli -rpcwallet=pool "$@" 2>/dev/null || cli "$@"; }
trap 'cli stop >/dev/null 2>&1; pkill -9 -f "$DIR/pool" 2>/dev/null; cli2 stop >/dev/null 2>&1; cleanup' EXIT

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

# Pre-fund with LEGACY blocks (real time, pre-activation) so the wallet has mature
# coins for sendmany. Then jump both nodes' clocks past the KAWPOW activation time
# so every block built afterwards — including the pool's — is a real KAWPOW block.
w generatetoaddress 120 "$POOL_ADDR" >/dev/null 2>&1
cli  setmocktime $KAWPOW_TIME >/dev/null 2>&1
cli2 setmocktime $KAWPOW_TIME >/dev/null 2>&1
log "$SYM: node up height=$(cli getblockcount) pool=$POOL_ADDR miner=$MINER_ADDR (kawpow activated via mocktime)"

log "$SYM: building kawpow pool + e2ervnminer (-tags kawpow)"
build_pool "$DIR/pool" kawpow || { fail "$SYM: pool build failed"; exit 1; }
build_tool e2ervnminer "$DIR/miner" kawpow || { fail "$SYM: miner build failed"; exit 1; }

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

# Mine until the pool's KAWPOW block is accepted (chain grows). The miner is
# one-shot per run, so loop; the pool records the pending block itself on accept.
H0=$(cli getblockcount)
LANDED=0
for attempt in $(seq 1 8); do
  with_timeout 40 "$DIR/miner" -pool 127.0.0.1:$SPORT -worker "$MINER_ADDR" >>"$DIR/miner.log" 2>&1
  sleep 1
  if [ "$(cli getblockcount)" -gt "$H0" ]; then LANDED=1; break; fi
done

if [ "$LANDED" != 1 ]; then
  fail "$SYM: pool never landed an accepted block"
  echo "--- header size (should be 120 = kawpow) ---" >&2
  hh=$(cli getblockheader "$(cli getbestblockhash)" false 2>/dev/null); echo "tip header = $(( ${#hh} / 2 )) bytes" >&2
  grep -iE "block candidate|rejected the block|Block decode|error with daemon" "$DIR/pool.log" | tail -6 >&2
  grep -iE "ERROR|reject|CheckBlock|high-hash|bad-|proof of work" "$DIR"/regtest/debug.log 2>/dev/null | tail -12 >&2
  exit 1
fi
log "$SYM: pool mined a real KAWPOW block, node accepted it (height $H0 -> $(cli getblockcount))"

paid=""
for i in $(seq 1 30); do
  paid=$(redis-cli hget "$COIN:payouts" "$MINER_ADDR" 2>/dev/null)
  [ -n "$paid" ] && python3 -c "import sys;sys.exit(0 if float('$paid')>0 else 1)" 2>/dev/null && break
  sleep 2
done
pkill -9 -f "$DIR/pool" 2>/dev/null

if [ -n "$paid" ] && python3 -c "import sys;sys.exit(0 if float('$paid')>0 else 1)" 2>/dev/null; then
  received=$(w getreceivedbyaddress "$MINER_ADDR" 0 2>/dev/null)
  ok "$SYM (kawpow): mined a real KAWPOW block → pool recorded it → payer paid the finder — payout=$paid received=$received"
  exit 0
fi
fail "$SYM (kawpow): block landed but the miner was not paid"
echo "--- pending / payouts ---" >&2
redis-cli smembers "$COIN:blocks:pending" >&2; redis-cli hgetall "$COIN:payouts" >&2
grep -iE "payment|sendmany|insufficient|orphan|gettransaction|coinbase" "$DIR/pool.log" | tail -20 >&2
exit 1
