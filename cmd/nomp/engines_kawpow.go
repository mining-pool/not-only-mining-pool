package main

// The KawPow (Ravencoin) engine is pure Go with tiny dependencies (powkit is
// already linked for verthash), so it is registered unconditionally — unlike
// ethash, which drags in go-ethereum and hides behind `-tags ethash`.
import _ "github.com/mining-pool/not-only-mining-pool/engine/kawpow"
