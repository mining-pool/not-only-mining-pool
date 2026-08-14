package coins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/algorithm"
	"github.com/mining-pool/not-only-mining-pool/config"
)

// algorithms shipped as templates but registered only under a build tag (cgo).
// They are legitimately absent from the default build's registry.
var pendingBindingAlgos = map[string]bool{
	"neoscrypt": true, // registered with -tags neoscrypt (github.com/sparkspay/go-neoscrypt)
}

// TestCoinTemplates validates every shipped coin config template: it must be
// well-formed JSON, decode into config.Options, name an algorithm that is
// either registered or a known pending-binding algorithm, and carry the minimal
// fields the pool needs to boot.
func TestCoinTemplates(t *testing.T) {
	files, err := filepath.Glob("config.*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no coin templates found (expected coins/config.*.json)")
	}

	for _, f := range files {
		f := f
		t.Run(f, func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}

			var opt config.Options
			if err := json.Unmarshal(raw, &opt); err != nil {
				t.Fatalf("invalid JSON / schema: %v", err)
			}

			if opt.Coin == nil || opt.Coin.Name == "" || opt.Coin.Symbol == "" {
				t.Fatal("coin.name and coin.symbol are required")
			}
			if opt.Algorithm == nil || opt.Algorithm.Name == "" {
				t.Fatal("algorithm.name is required")
			}

			algo := strings.ToLower(opt.Algorithm.Name)
			if !algorithm.IsSupported(algo) && !pendingBindingAlgos[algo] {
				t.Fatalf("algorithm %q is neither registered nor a known pending-binding algorithm", algo)
			}

			if len(opt.Daemons) == 0 || opt.Daemons[0].Port == 0 {
				t.Fatal("at least one daemon with a port is required")
			}
			if len(opt.Ports) == 0 {
				t.Fatal("at least one stratum port is required")
			}
			for port, p := range opt.Ports {
				if port <= 0 || port > 65535 {
					t.Fatalf("invalid stratum port %d", port)
				}
				if p.Diff <= 0 {
					t.Fatalf("port %d: starting diff must be > 0", port)
				}
				if p.VarDiff != nil && p.VarDiff.MinDiff > p.VarDiff.MaxDiff {
					t.Fatalf("port %d: minDiff > maxDiff", port)
				}
			}
			if opt.PoolAddress == nil || opt.PoolAddress.Address == "" {
				t.Fatal("poolAddress is required")
			}

			// x11/keccak-style coins must NOT use the sha256d block hasher, and
			// sha256d/scrypt coins normally do — a common config mistake.
			if algo == "x11" && opt.Algorithm.SHA256dBlockHasher {
				t.Errorf("%s uses x11: sha256dBlockHasher should be false", algo)
			}
		})
	}
}
