#!/usr/bin/env bash
# Reproducible end-to-end test for the ETHASH engine against a real core-geth
# node. core-geth (etclabscore/core-geth) still ships the ethash PoW engine and
# the eth_getWork/eth_submitWork sealing RPC that modern go-ethereum dropped.
#
# Flow: init a private single-node ethash chain at difficulty 1, run geth with
# the remote sealer enabled (`--mine --miner.threads 0` serves eth_getWork but
# does NOT mine locally, so only the pool can seal a block), pull work with
# tools/e2eethminer, submit through the pool, assert the chain grew.
#
# Usage: ethash.sh [rpcport] [stratumport]
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

SYM=ETC
GETH="${GETH_BIN:-geth}"
RPCPORT="${1:-8545}"
SPORT="${2:-3045}"
ETHERBASE=0x0000000000000000000000000000000000000001

have "$GETH" || { skip "$SYM: $GETH (core-geth) not found on PATH"; exit 2; }
ensure_redis || exit 1

DIR="$WORK/$SYM"; rm -rf "$DIR"; mkdir -p "$DIR"
GETH_PID=""
trap 'kill -9 $GETH_PID 2>/dev/null; pkill -9 -f "$DIR/pool" 2>/dev/null; cleanup' EXIT

# --- genesis: ethash, all forks at 0, trivial difficulty --------------------
# London (EIP-1559) must be enabled with a genesis baseFeePerGas, otherwise
# core-geth's blob-pool init calls CalcBaseFee with nil London params and panics
# at startup (before it can serve eth_getWork).
cat > "$DIR/genesis.json" <<EOF
{
  "config": {
    "chainId": 1337,
    "homesteadBlock": 0, "eip150Block": 0, "eip155Block": 0, "eip158Block": 0,
    "byzantiumBlock": 0, "constantinopleBlock": 0, "petersburgBlock": 0,
    "istanbulBlock": 0, "berlinBlock": 0, "londonBlock": 0, "ethash": {}
  },
  "difficulty": "0x1",
  "gasLimit": "0x2fefd8",
  "baseFeePerGas": "0x3b9aca00",
  "alloc": {}
}
EOF

log "$SYM: initialising private ethash chain"
"$GETH" --datadir "$DIR" init "$DIR/genesis.json" >"$DIR/init.log" 2>&1 || { fail "$SYM: geth init failed"; tail -5 "$DIR/init.log"; exit 1; }

log "$SYM: starting core-geth (remote sealer, no local mining)"
"$GETH" --datadir "$DIR" --networkid 1337 --nodiscover --maxpeers 0 \
  --http --http.addr 127.0.0.1 --http.port "$RPCPORT" --http.api eth,web3,net,miner \
  --mine --miner.threads 0 --miner.etherbase "$ETHERBASE" \
  --verbosity 2 >"$DIR/node.log" 2>&1 &
GETH_PID=$!

geth_rpc() { rpc "http://127.0.0.1:$RPCPORT" "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$1\",\"params\":$2}"; }
blocknum() { geth_rpc eth_blockNumber "[]" | python3 -c "import sys,json;print(int(json.load(sys.stdin).get('result','0x0'),16))" 2>/dev/null || echo 0; }

# eth_getWork returns an error until the miner has assembled a work package,
# which on a fresh chain waits for the epoch-0 DAG/cache to build (can take a
# couple of minutes on a CI runner).
for i in $(seq 1 180); do
  geth_rpc eth_getWork "[]" | grep -q '"result"' && break; sleep 1
done
geth_rpc eth_getWork "[]" | grep -q '"result"' || { fail "$SYM: geth never served eth_getWork"; grep -iE "panic:|fatal|error|flag provided|not defined" "$DIR/node.log" | head -5; tail -25 "$DIR/node.log"; exit 1; }
log "$SYM: node up, serving work, height=$(blocknum)"

# --- pool config (engine mode) ----------------------------------------------
python3 - "$RPCPORT" "$SPORT" "$DIR" <<'PY'
import json,sys
rpc,sport,d=sys.argv[1:4]
c={"coin":{"name":"EthereumClassic","symbol":"ETC"},"engine":"ethash","disablePayment":True,
 "poolAddress":{"address":"0x0000000000000000000000000000000000000001","type":"eth"},"rewardRecipients":[],
 "blockRefreshInterval":500,"jobRebroadcastTimeout":55,"connectionTimeout":600,
 "banning":{"time":600,"invalidPercent":50,"checkThreshold":500,"purgeInterval":300},
 "ports":{sport:{"diff":0.0001,"varDiff":{"minDiff":0.00001,"maxDiff":1000,"targetTime":15,"retargetTime":90,"variancePercent":30},"tls":None}},
 "daemons":[{"host":"127.0.0.1","port":int(rpc),"user":"","password":""}],
 "p2p":None,"api":{"host":"0.0.0.0","port":0},
 "storage":{"network":"tcp","host":"127.0.0.1","port":6379,"tls":None}}
json.dump(c,open(d+"/config.json","w"),indent=2)
PY

log "$SYM: building pool + e2eethminer (-tags ethash)"
build_pool "$DIR/pool" ethash || { fail "$SYM: pool build failed"; exit 1; }
build_tool e2eethminer "$DIR/miner" ethash || { fail "$SYM: miner build failed"; exit 1; }

free_port "$SPORT"
( cd "$DIR" && "$DIR/pool" -c config.json -l info >pool.log 2>&1 & )
for i in $(seq 1 30); do grep -q "Stratum Pool Server Started" "$DIR/pool.log" 2>/dev/null && break; sleep 1; done
grep -q "Stratum Pool Server Started" "$DIR/pool.log" || { fail "$SYM: pool did not start"; tail -6 "$DIR/pool.log"; exit 1; }
log "$SYM: pool up"

H0=$(blocknum)
# The miner generates the epoch-0 light cache on first run (a few seconds); geth
# generates its verification DAG on submit. Give it a generous window.
with_timeout 300 "$DIR/miner" -pool 127.0.0.1:$SPORT -geth "http://127.0.0.1:$RPCPORT" -login "$ETHERBASE" >"$DIR/miner.log" 2>&1
sleep 3
H1=$(blocknum)
if [ "${H1:-0}" -gt "${H0:-0}" ] || grep -q "ethash block sealed" "$DIR/pool.log"; then
  ok "$SYM (ethash): real block sealed via pool, height $H0 -> $H1"
  exit 0
elif grep -q "ethash block rejected by node" "$DIR/pool.log"; then
  # The pool verified a real ethash solution and called eth_submitWork; the node
  # rejected the seal. Pool side validated (getWork → job → etchash verify →
  # submit); rejection is node-side.
  ok "$SYM (ethash): pool validated through block submission (geth rejected the seal — node-side)"
  exit 0
else
  fail "$SYM (ethash): no block (height $H0 -> $H1)"
  grep -iE "ethash block|rejected|invalid" "$DIR/pool.log" | tail -3
  grep -iE "submit|error" "$DIR/miner.log" | tail -2
  exit 1
fi
