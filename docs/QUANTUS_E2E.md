# Quantus end-to-end validation

Validated on 2026-09-12, macOS ARM64, using an isolated local development chain.
No mainnet funds or external mining pool were used.

| Component | Version |
|---|---|
| quantus-node | 1.0.1, `f1176cea6a6` |
| Official quantus-miner | 4.2.0, four CPU workers |
| Quantus signing library | 0.3.0, source `539e3474e14d0dd5c2039c95e77e996e9d12bf93` |
| Accounting | Redis AOF, `appendfsync always` |

The reproducible run confirmed **128 pool blocks**. A previously unendowed
recipient received **1,230,000,000,000 base units (1.23 QTC)**, exactly matching
the finalized payment ledger.

Payment transaction:
`0xe55ba877fe7fa764530c513235af1781d8473812d2178a5658e49b14077da819`

The transaction was included at block **114**. The test killed the pool with a
persisted, unconfirmed signed payment and restarted it against the same Redis.
It recovered the same transaction, observed finalized `ExtrinsicSuccess`, and
debited the balance exactly once. Repeated scans did not create another receipt.
This transaction exists only on the temporary development chain.

Other checks covered real TCP login/share submission, invalid address rejection,
duplicate and malformed nonce rejection, the official miner's QUIC transport,
Poseidon2 block verification, finalized reward attribution, an ML-DSA signed
`utility.batchAll` transfer, and payment API redaction.

Run after [installing the signing bridge](../engine/quantus/README.md#自动支付):

```sh
python3 scripts/e2e/quantus.py \
  --node /path/to/quantus-node \
  --miner /path/to/quantus-miner
```

The script writes logs and `result.json` to a new temporary directory and stops
only the processes it starts. Each run uses different ports and block hashes.

SRBMiner GPU binaries, production throughput, and wormhole reward collection
were not exercised. The payout wallet was funded by the development genesis;
production reward collection requires the official CLI and its ZK/indexer services.
