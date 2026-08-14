//go:build kaspa
// +build kaspa

package main

// Building with `-tags kaspa` links the Kaspa engine and its kaspad dependency
// (gRPC + consensus code — heavy, hence the tag, like ethash/randomx).
import _ "github.com/mining-pool/not-only-mining-pool/engine/kaspa"
