// JSON-in/JSON-out bridge. Private seed material is read from a file, never argv.
import fs from 'node:fs';
import { ApiPromise, HttpProvider } from '@polkadot/api';
import { blake2AsHex, decodeAddress, encodeAddress } from '@polkadot/util-crypto';
import { account, signCall } from '@quantus-network/wasm';

let api;
try {
  const req = JSON.parse(fs.readFileSync(0, 'utf8'));
  const rpc = async (method, params = []) => {
    const res = await fetch(req.rpc, { method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }), signal: AbortSignal.timeout(15000) });
    if (!res.ok) throw new Error(`RPC HTTP ${res.status}`);
    const body = await res.json();
    if (body.error) throw new Error(`${method}: ${body.error.message}${body.error.data ? ': ' + JSON.stringify(body.error.data) : ''}`);
    return body.result;
  };
  const address = value => encodeAddress(decodeAddress(value, false, 189), 189);
  if (req.op === 'broadcast') {
    console.log(JSON.stringify({ hash: await rpc('author_submitExtrinsic', [req.raw]) }));
  } else {
    api = await ApiPromise.create({ provider: new HttpProvider(req.rpc), noInitWarn: true, types: { U512: '[u8;64]' } });
    const genesis = await rpc('chain_getBlockHash', [0]);
    if (req.genesis && req.genesis !== genesis) throw new Error('Genesis hash mismatch');
    const finalizedHash = await rpc('chain_getFinalizedHead');
    const finalized = Number((await rpc('chain_getHeader', [finalizedHash])).number);
    if (req.op === 'scan') {
      const blocks = [];
      for (let height = req.from; height <= Math.min(finalized, req.from + 31); height++) {
        const hash = await rpc('chain_getBlockHash', [height]);
        const { block } = await rpc('chain_getBlock', [hash]);
        const at = await api.at(hash);
        const events = await at.query.system.events();
        const rewards = [], outcomes = {};
        for (const { phase, event } of events) {
          if (event.section === 'miningRewards' && event.method === 'MinerRewarded')
            rewards.push({ miner: address(event.data[0].toString()), amount: event.data[1].toString() });
          if (phase.isApplyExtrinsic && event.section === 'system') {
            if (event.method === 'ExtrinsicSuccess') outcomes[phase.asApplyExtrinsic.toNumber()] = true;
            if (event.method === 'ExtrinsicFailed') outcomes[phase.asApplyExtrinsic.toNumber()] = false;
          }
        }
        blocks.push({ height, hash, header: block.header, rewards,
          transactions: block.extrinsics.map((raw, i) => ({ hash: blake2AsHex(raw, 256), success: outcomes[i] ?? null })) });
      }
      console.log(JSON.stringify({ genesis, finalized, blocks }));
    } else if (req.op === 'info' || req.op === 'prepare') {
      const stat = fs.statSync(req.seedFile);
      if (!stat.isFile() || (stat.mode & 0o077)) throw new Error('seedFile must be a regular file with mode 0600');
      const seedHex = fs.readFileSync(req.seedFile, 'utf8').trim();
      if (!/^[0-9a-fA-F]{64}$/.test(seedHex)) throw new Error('seedFile must contain a 32-byte hex seed');
      const seed = Buffer.from(seedHex, 'hex');
      const sender = account(seed).address;
      const { nonce, data } = await api.query.system.account(sender);
      const info = { genesis, finalized, sender, nonce: nonce.toString(), free: data.free.toString(), frozen: data.frozen.toString(), existentialDeposit: api.consts.balances.existentialDeposit.toString() };
      if (req.op === 'prepare') {
        if (req.sender !== sender) throw new Error('Signing wallet changed');
        if (!req.outputs?.length || req.outputs.length > 64) throw new Error('Expected 1..64 outputs');
        const calls = req.outputs.map(({ address: dest, amount }) => {
          if (!/^[0-9]+$/.test(amount) || BigInt(amount) <= 0n || BigInt(amount) >= 1n << 128n) throw new Error('Invalid u128 amount');
          if (address(dest) === sender) throw new Error('Payout to signing wallet is not allowed');
          return api.registry.createType('Call', { callIndex: api.tx.balances.transferKeepAlive.callIndex,
            args: { dest: address(dest), value: BigInt(amount) } });
        });
        const total = req.outputs.reduce((sum, o) => sum + BigInt(o.amount), 0n);
        if (BigInt(data.free.toString()) - BigInt(data.frozen.toString()) < total + BigInt(req.reserve))
          throw new Error('Insufficient hot-wallet balance including reserve');
        const call = api.registry.createType('Call', { callIndex: api.tx.utility.batchAll.callIndex, args: { calls } }).toHex();
        const raw = '0x' + Buffer.from(signCall(seed, call, { nonce: BigInt(nonce.toString()), genesisHash: genesis,
          specVersion: api.runtimeVersion.specVersion.toNumber(), transactionVersion: api.runtimeVersion.transactionVersion.toNumber() })).toString('hex');
        const fee = BigInt((await rpc('payment_queryInfo', [raw])).partialFee);
        if (BigInt(data.free.toString()) - BigInt(data.frozen.toString()) < total + BigInt(req.reserve) + fee)
          throw new Error('Insufficient hot-wallet balance including transaction fee and reserve');
        Object.assign(info, { raw, hash: blake2AsHex(raw, 256) });
      }
      seed.fill(0);
      console.log(JSON.stringify(info));
    } else throw new Error('Unknown wallet operation');
  }
} catch (err) {
  console.error(err.message);
  process.exitCode = 1;
} finally {
  if (api) await api.disconnect();
}
