// Command e2everthashgen generates the ~1.2GB verthash.dat under ~/.powcache so
// the e2e image can bake it once at build time instead of every VTC pool start
// (the powkit verthash lib does not create its cache dir, so do that first).
package main

import (
	"os"
	"path/filepath"

	"github.com/mining-pool/not-only-mining-pool/algorithm"
)

func main() {
	home, _ := os.UserHomeDir()
	_ = os.MkdirAll(filepath.Join(home, ".powcache"), 0o755)
	algorithm.Warmup("verthash")
}
