#!/usr/bin/env python3
"""Real Quantus dev-chain E2E. Requires node/miner binaries and wallet setup.

python3 scripts/e2e/quantus.py --node /path/quantus-node --miner /path/quantus-miner
Only starts isolated local services; uses the publicly known dev seed [0;32].
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--node', required=True)
parser.add_argument('--miner', required=True)
parser.add_argument('--timeout', type=int, default=600)
args = parser.parse_args()
repo = Path(__file__).resolve().parents[2]
run = Path(tempfile.mkdtemp(prefix='nomp-quantus-e2e-'))
processes = []
logs = []
deadline = time.monotonic() + args.timeout
print(f'Artifacts: {run}', flush=True)


def port():
    with socket.socket() as tcp, socket.socket(type=socket.SOCK_DGRAM) as udp:
        tcp.bind(('127.0.0.1', 0))
        p = tcp.getsockname()[1]
        udp.bind(('127.0.0.1', p))
        return p


def start(name, command):
    log = open(run / (name + '.log'), 'ab')
    logs.append(log)
    p = subprocess.Popen([str(v) for v in command], stdout=log, stderr=log, cwd=repo)
    processes.append(p)
    return p


def wait_for(label, predicate):
    last = None
    while time.monotonic() < deadline:
        try:
            value = predicate()
            if value:
                print(label, flush=True)
                return value
        except (OSError, ValueError, KeyError) as err:
            last = err
        time.sleep(0.5)
    raise TimeoutError(f'{label}: {last}; inspect {run}')


def rpc(method, params=None):
    data = json.dumps({'jsonrpc': '2.0', 'id': 1, 'method': method, 'params': params or []}).encode()
    req = urllib.request.Request(rpc_url, data, {'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=10) as response:
        obj = json.load(response)
    if 'error' in obj:
        raise ValueError(obj['error'])
    return obj['result']


def ledger():
    raw = subprocess.check_output(['redis-cli', '-p', str(redis_port), 'GET', 'QuantusE2E:quantus:ledger'])
    return json.loads(raw)


def status():
    with urllib.request.urlopen(f'http://127.0.0.1:{api_port}/payments', timeout=10) as res:
        return json.load(res)


def response(stream, request_id):
    while True:
        line = stream.readline()
        if not line:
            raise ValueError('Stratum connection closed')
        obj = json.loads(line)
        if obj.get('id') == request_id:
            return obj


def send(stream, request_id, method, params):
    stream.write(json.dumps({'jsonrpc': '2.0', 'id': request_id, 'method': method, 'params': params}).encode() + b'\n')
    stream.flush()
    return response(stream, request_id)


try:
    rpc_port, miner_port, peer_port, redis_port, quic_port, tcp_port, api_port, metrics_port = [port() for _ in range(8)]
    rpc_url = f'http://127.0.0.1:{rpc_port}'
    start('node', [args.node, '--dev', '--validator', '--force-authoring', '--base-path', run / 'chain',
                   '--rewards-inner-hash', '0x' + '42' * 32, '--miner-listen-port', miner_port,
                   '--rpc-port', rpc_port, '--port', peer_port, '--rpc-methods', 'unsafe', '--no-telemetry', '--no-prometheus'])
    wait_for('Development node ready', lambda: rpc('chain_getHeader'))
    node_dir = run / 'chain/chains/dev'
    wait_for('Node miner certificate ready', lambda: (node_dir / 'miner-tls-cert-sha256').exists())
    reward = re.search(r'Rewards wormhole address: (\w+)', (run / 'node.log').read_text())[1]
    start('redis', ['redis-server', '--bind', '127.0.0.1', '--port', redis_port, '--appendonly', 'yes',
                    '--appendfsync', 'always', '--dir', run])
    wait_for('Durable Redis ready', lambda: subprocess.run(['redis-cli', '-p', str(redis_port), 'ping'], capture_output=True).stdout.strip() == b'PONG')
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes',
                    '-keyout', str(run / 'key.pem'), '-out', str(run / 'cert.pem'), '-days', '1', '-subj', '/CN=localhost'], check=True, capture_output=True)
    pin = hashlib.sha256(ssl.PEM_cert_to_DER_cert((run / 'cert.pem').read_text())).hexdigest()
    (run / 'pin').write_text(pin)
    (run / 'seed').write_text('00' * 32)
    (run / 'seed').chmod(0o600)
    # Seed [7;32], initially unendowed; no private material is needed to receive.
    miner_address = 'qzmAAFv4c7tprk5UyJfav4hgkGR8xyckGeZL2KhwM1FWMW1gk'
    conf = json.loads((repo / 'engine/quantus/config.quantus.example.json').read_text())
    conf.update(coin={'name': 'QuantusE2E', 'symbol': 'QTC'}, disablePayment=False,
                payment={'interval': 1, 'payMode': 'prop'})
    conf['storage']['port'] = redis_port
    conf['api'] = {'host': '127.0.0.1', 'port': api_port}
    conf['ports'] = {str(quic_port): {'protocol': 'quic', 'diff': 131072, 'varDiff': None,
                                    'tls': {'certFile': str(run / 'cert.pem'), 'keyFile': str(run / 'key.pem')}},
                     str(tcp_port): {'protocol': 'stratum', 'diff': 1, 'varDiff': None, 'tls': None}}
    conf['quantus'] = {'nodeAddress': f'127.0.0.1:{miner_port}', 'authTokenFile': str(node_dir / 'miner-auth-token'),
                       'tlsCertSHA256File': str(node_dir / 'miner-tls-cert-sha256'),
                       'payments': {'rpcUrl': rpc_url, 'genesisHash': rpc('chain_getBlockHash', [0]), 'rewardAddress': reward,
                                    'walletScript': str(repo / 'tools/quantus-wallet/wallet.mjs'), 'seedFile': str(run / 'seed'),
                                    'minPaymentUnits': '1000000000', 'reserveUnits': '1000000000000'}}
    (run / 'pool.json').write_text(json.dumps(conf, indent=2))
    subprocess.run(['go', 'build', '-o', str(run / 'nomp'), './cmd/nomp'], cwd=repo, check=True)
    pool_command = [run / 'nomp', '-c', run / 'pool.json']
    pool = start('pool', pool_command)
    wait_for('Pool payment API ready', status)
    with socket.create_connection(('127.0.0.1', tcp_port), timeout=10) as conn:
        with conn.makefile('rwb') as stream:
            assert send(stream, 1, 'login', {'login': 'invalid.rig'}).get('error')
            login = send(stream, 2, 'login', {'login': miner_address + '.tcp'})['result']
            job = login['job']
            if job is None:
                while job is None:
                    job = json.loads(stream.readline()).get('params', {}).get('job')
            params = {'id': login['id'], 'job_id': job['job_id'], 'nonce': login['id'] + os.urandom(60).hex()}
            # At share difficulty 1, every hash except the maximum U512 qualifies.
            result = send(stream, 3, 'submit', params)
            assert not result.get('error'), result
            assert send(stream, 4, 'submit', params).get('error'), 'duplicate accepted'
            params['nonce'] = '00'
            assert send(stream, 5, 'submit', params).get('error'), 'malformed nonce accepted'
    print('TCP login, real share, duplicate and malformed nonce checks passed', flush=True)
    miner = start('miner', [args.miner, 'serve', '--node-addr', f'127.0.0.1:{quic_port}', '--auth-token', miner_address + '.quic',
                             '--tls-cert-sha256-file', run / 'pin', '--cpu-workers', 4, '--gpu-devices', 0, '--metrics-port', metrics_port])
    pending = wait_for('Signed payout persisted', lambda: (ledger().get('intent') or {}).get('hash'))
    pool.kill()
    pool.wait(timeout=10)
    # Crash the pool with a pending signed transaction, then recover from Redis.
    pool = start('pool', pool_command)
    wait_for('Pool restarted', status)
    receipt = wait_for('Original transaction finalized exactly once after restart',
                       lambda: next((p for p in (ledger().get('paid') or []) if p['hash'] == pending), None))
    miner.terminate()
    miner.wait(timeout=20)
    # Let any in-flight solution finish, then prove repeated payment scans cannot
    # consume the receipt twice without further chain progress.
    time.sleep(2)
    before = ledger()
    time.sleep(4)
    after = ledger()
    assert len([p for p in after['paid'] if p['hash'] == pending]) == 1
    assert before['paid'] == after['paid'], 'repeated scan changed finalized receipts'
    assert len(after['blocks']) >= 100
    # Query balances at exactly the ledger's finalized cursor (not the best tip,
    # which can already contain the next unfinalized payment).
    final_hash = rpc('chain_getBlockHash', [after['cursor']])
    js = """
import { ApiPromise, HttpProvider } from '@polkadot/api';
const api = await ApiPromise.create({provider:new HttpProvider(process.argv[1]),noInitWarn:true,types:{U512:'[u8;64]'}});
const at=await api.at(process.argv[2]); const a=await at.query.system.account(process.argv[3]);
console.log(a.data.free.toString()); await api.disconnect();
"""
    balance = int(subprocess.check_output(['node', '--input-type=module', '-e', js, rpc_url, final_hash, miner_address], cwd=repo / 'tools/quantus-wallet', text=True).strip())
    paid = sum(int(o['amount']) for p in after['paid'] for o in p['outputs'] if o['address'] == miner_address)
    assert balance == paid > 0, (balance, paid)
    public = status()
    assert 'raw' not in json.dumps(public), 'API exposed signed transaction'
    result = {'nodeVersion': subprocess.check_output([args.node, '--version'], text=True).strip(),
              'minerVersion': subprocess.check_output([args.miner, '--version'], text=True).strip(),
              'finalizedHeight': after['cursor'], 'confirmedPoolBlocks': len(after['blocks']),
              'recoveredTransaction': pending, 'receipt': receipt, 'recipient': miner_address,
              'finalizedRecipientBalanceUnits': str(balance), 'paidUnits': str(paid),
              'checks': ['TCP valid/duplicate/malformed shares', 'official QUIC miner', 'real PoW block import',
                         'finalized reward attribution', 'ML-DSA signed atomic payout', 'crash recovery',
                         'no duplicate debit', 'exact finalized balance', 'redacted payment API']}
    (run / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2), flush=True)
finally:
    for p in reversed(processes):
        if p.poll() is None:
            p.terminate()
            try:
                p.wait(timeout=15)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
    for log in logs:
        log.close()
