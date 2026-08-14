//go:build ethash
// +build ethash

package main

// Building with `-tags ethash` links the Ethash/Etchash engine (and its
// go-ethereum/go-etchash dependencies) into the binary. The default build
// stays lean for Bitcoin-family pools.
import _ "github.com/mining-pool/not-only-mining-pool/engine/ethash"
