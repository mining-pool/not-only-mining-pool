// Command e2ekasminer is a minimal kHeavyHash stratum miner (kaspa-stratum-bridge
// dialect) for end-to-end testing the kaspa engine. It uses powkit's heavyhash,
// independently cross-checked against kaspad's own pow in the engine's tests.
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/sencha-dev/powkit/heavyhash"
)

var (
	poolAddr = flag.String("pool", "127.0.0.1:3044", "stratum host:port")
	worker   = flag.String("worker", "miner", "worker name")
	minDiff  = flag.Float64("mindiff", 0, "extra difficulty floor to try to hit a block")
)

var pow256 = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil)

func main() {
	flag.Parse()
	conn, err := net.Dial("tcp", *poolAddr)
	must(err)
	defer conn.Close()
	br := bufio.NewReader(conn)

	send(conn, 1, "mining.subscribe", []interface{}{"e2e"})
	readByID(br, 1)
	send(conn, 2, "mining.authorize", []interface{}{*worker, "x"})

	shareDiff := 0.001
	var jobID, largeJob string
	for jobID == "" {
		m := readLine(br)
		switch m["method"] {
		case "mining.set_difficulty":
			if p, ok := m["params"].([]interface{}); ok && len(p) > 0 {
				shareDiff, _ = p[0].(float64)
			}
		case "mining.notify":
			p := m["params"].([]interface{})
			jobID = p[0].(string)
			largeJob = p[1].(string)
		}
	}
	fmt.Printf("[kas] job=%s shareDiff=%v\n", jobID, shareDiff)

	prePow, timestamp := parseLargeJob(largeJob)
	client := heavyhash.NewKaspa()

	start := time.Now()
	for nonce := uint64(0); ; nonce++ {
		digest, err := client.Compute(prePow, timestamp, nonce)
		must(err)
		powValue := new(big.Int).SetBytes(digest)
		diff, _ := new(big.Float).Quo(new(big.Float).SetInt(pow256), new(big.Float).SetInt(powValue)).Float64()
		if diff >= shareDiff && diff >= *minDiff {
			fmt.Printf("[kas] solution nonce=%x diff=%.4f after %d hashes in %s\n", nonce, diff, nonce+1, time.Since(start))
			send(conn, 4, "mining.submit", []interface{}{*worker, jobID, fmt.Sprintf("%016x", nonce)})
			fmt.Printf("[kas] submit response: %v\n", readByID(br, 4))
			return
		}
		if nonce > 5_000_000 {
			fmt.Println("[kas] gave up")
			os.Exit(1)
		}
	}
}

// parseLargeJob decodes 4×BE-uint64(prePowHash) || uint64(timestamp).
func parseLargeJob(s string) ([]byte, int64) {
	prePow := make([]byte, 32)
	for i := 0; i < 4; i++ {
		w, _ := strconv.ParseUint(s[i*16:i*16+16], 16, 64)
		binary.BigEndian.PutUint64(prePow[i*8:], w)
	}
	ts, _ := strconv.ParseUint(s[64:80], 16, 64)
	return prePow, int64(ts)
}

func send(conn net.Conn, id int, method string, params []interface{}) {
	b, _ := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params})
	_, err := conn.Write(append(b, '\n'))
	must(err)
}

func readByID(br *bufio.Reader, id int) map[string]interface{} {
	for {
		m := readLine(br)
		if v, ok := m["id"].(float64); ok && int(v) == id {
			return m
		}
	}
}

func readLine(br *bufio.Reader) map[string]interface{} {
	line, err := br.ReadBytes('\n')
	must(err)
	var m map[string]interface{}
	if json.Unmarshal(line, &m) != nil {
		return map[string]interface{}{}
	}
	return m
}

func must(err error) {
	if err != nil {
		fmt.Println("[kas] error:", err)
		os.Exit(1)
	}
}
