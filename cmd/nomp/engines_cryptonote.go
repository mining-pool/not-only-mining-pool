package main

// The CryptoNote (Monero/RandomX) engine registers unconditionally, but its
// RandomX verifier links only with `-tags randomx` (cgo + librandomx.a); a
// default build refuses to start it with a clear error instead.
import _ "github.com/mining-pool/not-only-mining-pool/engine/cryptonote"
