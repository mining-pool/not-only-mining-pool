//go:build !randomx
// +build !randomx

package cryptonote

import (
	"strings"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/config"
)

// Without -tags randomx the engine must refuse to start with a clear message
// rather than panic or silently accept unverifiable shares.
func TestInitRefusesWithoutRandomX(t *testing.T) {
	err := New().Init(&config.Options{})
	if err == nil || !strings.Contains(err.Error(), "-tags randomx") {
		t.Fatalf("expected a build-tag hint error, got %v", err)
	}
}
