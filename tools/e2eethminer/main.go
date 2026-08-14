//go:build ethash
// +build ethash

// Command e2eethminer is a minimal etchash stratum miner (ethproxy dialect) for
// end-to-end testing the ethash engine. It pulls work from geth directly, finds
// a nonce with go-etchash, and submits through the pool. Build with -tags ethash.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	etchash "github.com/etclabscore/go-etchash"
	"github.com/ethereum/go-ethereum/common"
)

var (
	poolAddr = flag.String("pool", "127.0.0.1:3045", "stratum host:port")
	gethURL  = flag.String("geth", "http://127.0.0.1:8545", "geth rpc")
	login    = flag.String("login", "0x0000000000000000000000000000000000000001", "login")
)

func main() {
	flag.Parse()

	header, seed, target, blockNum := getWork()
	_ = seed
	fmt.Printf("[eth] work header=%s target=%s block=%d\n", header[:14], target.Text(16)[:12], blockNum)

	var ecip uint64 = 99999999
	light := etchash.New(&ecip, nil)

	headerHash := common.HexToHash(header)
	fmt.Println("[eth] searching (generates epoch-0 light cache)...")
	start := time.Now()
	var nonce uint64
	var mix common.Hash
	for {
		mixDigest, result := light.Compute(blockNum, headerHash, nonce)
		if result.Big().Cmp(target) <= 0 {
			mix = mixDigest
			break
		}
		nonce++
		if nonce > 5_000_000 {
			fmt.Println("[eth] gave up")
			os.Exit(1)
		}
	}
	fmt.Printf("[eth] solution nonce=%016x mix=%s after %s\n", nonce, mix.Hex()[:14], time.Since(start))

	conn, err := net.Dial("tcp", *poolAddr)
	must(err)
	defer conn.Close()
	br := bufio.NewReader(conn)

	send(conn, 1, "eth_submitLogin", []interface{}{*login, "x"})
	readByID(br, 1)
	send(conn, 4, "eth_submitWork", []interface{}{
		fmt.Sprintf("0x%016x", nonce), header, mix.Hex(),
	})
	fmt.Printf("[eth] submit response: %v\n", readByID(br, 4))
}

func getWork() (header, seed string, target *big.Int, blockNum uint64) {
	raw := rpc(`{"jsonrpc":"2.0","id":1,"method":"eth_getWork","params":[]}`)
	var r struct {
		Result []string `json:"result"`
	}
	must(json.Unmarshal(raw, &r))
	if len(r.Result) < 3 {
		must(fmt.Errorf("eth_getWork returned %v", r.Result))
	}
	target, _ = new(big.Int).SetString(strings.TrimPrefix(r.Result[2], "0x"), 16)
	if len(r.Result) >= 4 {
		n, _ := new(big.Int).SetString(strings.TrimPrefix(r.Result[3], "0x"), 16)
		blockNum = n.Uint64()
	}
	return r.Result[0], r.Result[1], target, blockNum
}

func rpc(body string) []byte {
	req, _ := http.NewRequest("POST", *gethURL, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	must(err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}

func send(conn net.Conn, id int, method string, params []interface{}) {
	b, _ := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params})
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
		if v, ok := m["id"]; ok {
			if f, ok := v.(float64); ok && int(f) == id {
				return m
			}
		}
	}
}

var _ = strconv.Itoa

func must(err error) {
	if err != nil {
		fmt.Println("[eth] error:", err)
		os.Exit(1)
	}
}
