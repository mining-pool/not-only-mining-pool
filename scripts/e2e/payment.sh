#!/usr/bin/env bash
# Reproducible end-to-end test for the PAYOUT processor against a real bitcoind
# regtest wallet: run a payment-enabled pool, mine a block to the pool address,
# mature the coinbase, and assert the pool actually pays the miner via sendmany
# (redis payouts hash + on-chain receipt).
#
# Usage: payment.sh [rpcport] [stratumport]
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

SYM=PAY
DAEMON=bitcoind CLI=bitcoin-cli
RPCPORT="${1:-18455}" SPORT="${2:-3050}"
COIN=Bitcoin

have "$DAEMON" || { skip "$SYM: $DAEMON not found on PATH"; exit 2; }
ensure_redis || exit 1
redis-cli del "$COIN:payouts" "$COIN:balances" "$COIN:blocks:pending" "$COIN:blocks:confirmed" >/dev/null 2>&1

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

# pre-fund the wallet so it can cover the payout tx fee, then let wall-clock catch up
w generatetoaddress 101 "$POOL_ADDR" >/dev/null 2>&1
for i in $(seq 1 20); do
  ct=$(cli getblocktemplate '{"rules":["segwit"]}' 2>/dev/null | python3 -c "import sys,json;print(json.load(sys.stdin).get('curtime',0))" 2>/dev/null || echo 0)
  [ "${ct:-0}" -le "$(( $(date +%s) + 3 ))" ] && break; sleep 1
done
log "$SYM: node up height=$(cli getblockcount) pool=$POOL_ADDR miner=$MINER_ADDR"

# --- payment-enabled pool config -------------------------------------------
python3 - "$POOL_ADDR" "$RPCPORT" "$SPORT" "$DIR" "$COIN" <<'PY'
import json,sys
addr,rpc,sport,d,coin=sys.argv[1:6]
c={"coin":{"name":coin,"symbol":"BTC"},"algorithm":{"name":"sha256d","multiplier":0,"sha256dBlockHasher":True},
 "disablePayment":False,
 "payment":{"interval":5,"minPayment":0.001,"daemon":0,"magnitude":1e8,"minConfirmations":100,
            "addressCheckMethod":"getaddressinfo","sendManyDummy":"","omitSendManyDummy":False},
 "poolAddress":{"address":addr,"type":"p2pkh"},"rewardRecipients":[],
 "blockRefreshInterval":500,"jobRebroadcastTimeout":55,"connectionTimeout":600,
 "banning":{"time":600,"invalidPercent":50,"checkThreshold":500,"purgeInterval":300},
 "ports":{sport:{"diff":0.0001,"varDiff":{"minDiff":0.00001,"maxDiff":1000,"targetTime":15,"retargetTime":90,"variancePercent":30},"tls":None}},
 "daemons":[{"host":"127.0.0.1","port":int(rpc),"user":"u","password":"p"}],
 "p2p":None,"api":{"host":"0.0.0.0","port":0},
 "storage":{"network":"tcp","host":"127.0.0.1","port":6379,"tls":None}}
json.dump(c,open(d+"/config.json","w"),indent=2)
PY

log "$SYM: building pool + e2eminer"
build_pool "$DIR/pool" || { fail "$SYM: pool build failed"; exit 1; }
build_tool e2eminer "$DIR/miner" || { fail "$SYM: miner build failed"; exit 1; }

free_port "$SPORT"
( cd "$DIR" && "$DIR/pool" -c config.json -l info >pool.log 2>&1 & )
wait_pool_started "$DIR/pool.log" || { fail "$SYM: pool did not start"; tail -30 "$DIR/pool.log"; exit 1; }
log "$SYM: pool up (payments enabled)"

# mine one block through the pool; its coinbase pays POOL_ADDR and the share is
# recorded under the miner's payout address.
H0=$(cli getblockcount)
with_timeout 240 "$DIR/miner" -pool 127.0.0.1:$SPORT -worker "$MINER_ADDR" -algo sha256d -coinbasehash sha256d -rpc "http://u:p@127.0.0.1:$RPCPORT" >"$DIR/miner.log" 2>&1
sleep 2
H1=$(cli getblockcount)
[ "${H1:-0}" -gt "${H0:-0}" ] || { fail "$SYM: pool did not mine a block ($H0 -> $H1)"; grep -iE "found block|reject" "$DIR/pool.log" | tail -3; exit 1; }
log "$SYM: pool mined block at height $H1; maturing coinbase (+100 blocks)"

# mature the pool's coinbase so its reward becomes spendable, then let the payer run.
w generatetoaddress 100 "$JUNK_ADDR" >/dev/null 2>&1
for i in $(seq 1 24); do
  redis-cli hget "$COIN:payouts" "$MINER_ADDR" 2>/dev/null | grep -qE "[0-9]" && break
  sleep 2
done

PAID=$(redis-cli hget "$COIN:payouts" "$MINER_ADDR" 2>/dev/null)
CONFIRMED=$(redis-cli scard "$COIN:blocks:confirmed" 2>/dev/null)
w generatetoaddress 1 "$JUNK_ADDR" >/dev/null 2>&1   # confirm the payout tx
RECEIVED=$(w getreceivedbyaddress "$MINER_ADDR" 1 2>/dev/null || echo 0)

if [ -n "$PAID" ] && python3 -c "import sys;sys.exit(0 if float('$PAID')>0 else 1)" 2>/dev/null; then
  ok "$SYM: pool paid miner $PAID (redis payouts), block confirmed=$CONFIRMED, on-chain received=$RECEIVED"
  exit 0
else
  fail "$SYM: no payout recorded (payouts=$PAID confirmed=$CONFIRMED received=$RECEIVED)"
  grep -iE "payment|sendmany|paid|payout|maturit|insufficient" "$DIR/pool.log" | tail -8
  exit 1
fi
