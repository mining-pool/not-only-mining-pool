// Command web-fixture serves the real NOMP HTTP API (api package, Redis
// storage, admin sessions, CORS, privacy filtering) on an in-memory Redis
// seeded with shares, so the pool-webpage frontend can be tested end to end
// without coin nodes. It is a test fixture: never run it in production.
//
//	NOMP_FIXTURE_ADMIN_PASSWORD=... go run ./scripts/web-fixture \
//	  -addr 127.0.0.1:20100 -origin http://127.0.0.1:4173
package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alicebob/miniredis/v2"
	"golang.org/x/crypto/bcrypt"

	"github.com/mining-pool/not-only-mining-pool/api"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/storage"
	"github.com/mining-pool/not-only-mining-pool/types"
)

// Fixed identities so browser tests can assert on them.
var miners = map[string][]string{
	"qtc1fixtureminer0alpha":   {"rig-a1", "rig-a2"},
	"qtc1fixtureminer1bravo":   {"rig-b1"},
	"qtc1fixtureminer2charlie": {"rig-c1"},
}

func ledger() map[string]interface{} {
	out := func(address, amount string) map[string]string {
		return map[string]string{"address": address, "amount": amount}
	}
	return map[string]interface{}{
		"decimals": 12, "finalizedHeight": 51234, "unconfirmedCandidates": 2,
		"balances": map[string]string{"qtc1fixtureminer0alpha": "1500000000000", "qtc1fixtureminer1bravo": "250000000000"},
		"pending": map[string]interface{}{"id": "batch-7", "hash": "0xfixturepending",
			"outputs": []interface{}{out("qtc1fixtureminer1bravo", "250000000000")}},
		"paid": []interface{}{
			map[string]interface{}{"hash": "0xfixturepaid1", "height": 51000,
				"outputs": []interface{}{out("qtc1fixtureminer0alpha", "2000000000000"), out("qtc1fixtureminer1bravo", "1000000000000")}},
		},
		"blocks": []interface{}{
			map[string]interface{}{"hash": "0xfixtureblock1", "height": 50990, "reward": "5000000000000"},
		},
	}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:20100", "API listen address")
	origins := flag.String("origin", "http://127.0.0.1:4173", "comma-separated allowed frontend origins")
	flag.Parse()

	password := os.Getenv("NOMP_FIXTURE_ADMIN_PASSWORD")
	if len(password) < 12 {
		log.Fatal("set NOMP_FIXTURE_ADMIN_PASSWORD (12+ bytes)")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "nomp-web-fixture")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	hashFile := filepath.Join(dir, "admin-password.hash")
	if err := os.WriteFile(hashFile, hash, 0o600); err != nil {
		log.Fatal(err)
	}

	redis, err := miniredis.Run()
	if err != nil {
		log.Fatal(err)
	}
	defer redis.Close()
	redisHost, redisPort, _ := net.SplitHostPort(redis.Addr())
	port, _ := strconv.Atoi(redisPort)
	db := storage.NewStorage("Quantus", &config.RedisOptions{Network: "tcp", Host: redisHost, Port: port})

	// Real share writes through the storage layer the stratum server uses.
	for miner, rigs := range miners {
		for i, rig := range rigs {
			for n := 0; n < 5; n++ {
				// Realistic Quantus share difficulty (1 difficulty = 1 hash).
				db.PutShare(&types.Share{Miner: miner, Rig: rig, Diff: float64(12_000_000 * (i + 1))}, true)
			}
		}
	}

	host, apiPort, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatal(err)
	}
	listenPort, _ := strconv.Atoi(apiPort)
	options := &config.Options{
		Coin:      &config.CoinOptions{Name: "Quantus", Symbol: "QTC", Testnet: true},
		Algorithm: &config.AlgorithmOptions{Name: "poseidon2", Multiplier: 1},
		Ports: map[int]*config.PortOptions{
			3333: {Protocol: "quic", Diff: 1},
			4444: {Protocol: "stratum", Diff: 8, TLS: &config.TLSServerOptions{CertFile: "/fixture/cert.pem", KeyFile: "/fixture/key.pem"}},
		},
		API: &config.APIOptions{
			Host: host, Port: listenPort,
			AllowedOrigins: strings.Split(*origins, ","),
			Admin: &config.AdminOptions{
				Username: "operator", PasswordHashFile: hashFile,
				SessionTTLSeconds: 600, AllowInsecureHTTP: true,
			},
		},
	}
	server, err := api.NewServer(options, db)
	if err != nil {
		log.Fatal(err)
	}
	server.SetHashrate(1, "H/s") // as engine/quantus reports
	server.RegisterPaymentStatus(func() (interface{}, error) { return ledger(), nil })
	server.Serve()

	// Give the asynchronous share writer a moment before announcing readiness.
	time.Sleep(200 * time.Millisecond)
	log.Printf("web fixture API ready on http://%s", *addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
}
