//go:build randomx
// +build randomx

// Command e2exmrminer is a minimal CryptoNote/RandomX stratum miner (XMRig dialect)
// for end-to-end testing the cryptonote engine against monerod regtest. Build
// with `-tags randomx` (links the prebuilt lib in github.com/mining-pool/go-randomx).
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	randomx "github.com/mining-pool/go-randomx"
)

var (
	poolAddr = flag.String("pool", "127.0.0.1:3040", "stratum host:port")
	login    = flag.String("login", "miner", "login (wallet address)")
	nonceOff = flag.Int("nonceoff", 39, "nonce offset in the hashing blob")
	netDiff  = flag.Uint64("diff", 200, "target difficulty to reach (network fixed-difficulty)")
)

var pow256 = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil)

func main() {
	flag.Parse()

	conn, err := net.Dial("tcp", *poolAddr)
	must(err)
	defer conn.Close()
	br := bufio.NewReader(conn)

	send(conn, 1, "login", map[string]interface{}{"login": *login, "pass": "x", "agent": "e2e/1.0"})
	resp := readByID(br, 1)
	res := resp["result"].(map[string]interface{})
	sessionID, _ := res["id"].(string)
	job := res["job"].(map[string]interface{})
	jobID := job["job_id"].(string)
	blob, _ := hex.DecodeString(job["blob"].(string))
	seed, _ := hex.DecodeString(job["seed_hash"].(string))
	fmt.Printf("[xmr] session=%s job=%s blob=%dB seed=%s\n", sessionID, jobID, len(blob), job["seed_hash"])

	fmt.Println("[xmr] initializing RandomX light VM (generates ~256MB cache)...")
	vm, err := randomx.NewLightVM(seed)
	must(err)
	defer vm.Close()

	start := time.Now()
	for nonce := uint32(0); ; nonce++ {
		binary.LittleEndian.PutUint32(blob[*nonceOff:*nonceOff+4], nonce)
		h, err := vm.Hash(seed, blob)
		must(err)
		hashNum := new(big.Int).SetBytes(reverse(h)) // little-endian 256-bit
		diff, _ := new(big.Float).Quo(new(big.Float).SetInt(pow256), new(big.Float).SetInt(hashNum)).Float64()
		if uint64(diff) >= *netDiff {
			nb := make([]byte, 4)
			binary.LittleEndian.PutUint32(nb, nonce)
			fmt.Printf("[xmr] solution nonce=%08x diff=%.0f after %d hashes in %s\n", nonce, diff, nonce+1, time.Since(start))
			send(conn, 2, "submit", map[string]interface{}{
				"id": sessionID, "job_id": jobID,
				"nonce": hex.EncodeToString(nb), "result": hex.EncodeToString(h),
			})
			fmt.Printf("[xmr] submit response: %v\n", readByID(br, 2))
			return
		}
		if nonce%256 == 255 {
			fmt.Printf("[xmr] ...%d hashes, best pass under diff %d\n", nonce+1, *netDiff)
		}
	}
}

func reverse(b []byte) []byte {
	o := make([]byte, len(b))
	for i := range b {
		o[len(b)-1-i] = b[i]
	}
	return o
}

func send(conn net.Conn, id int, method string, params interface{}) {
	b, _ := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params, "jsonrpc": "2.0"})
	_, err := conn.Write(append(b, '\n'))
	must(err)
}

func readByID(br *bufio.Reader, id int) map[string]interface{} {
	for {
		line, err := br.ReadBytes('\n')
		must(err)
		var m map[string]interface{}
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if v, ok := m["id"].(float64); ok && int(v) == id {
			return m
		}
	}
}

func must(err error) {
	if err != nil {
		fmt.Println("[xmr] error:", err)
		os.Exit(1)
	}
}
