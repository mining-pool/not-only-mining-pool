package main

// The Equihash engine (Zcash 200,9 / Flux ZelHash 125,4) is pure Go via
// powkit's blake2b-based verifier — no cache, no DAG, no cgo — so it is
// registered unconditionally like kawpow.
import _ "github.com/mining-pool/not-only-mining-pool/engine/equihash"
