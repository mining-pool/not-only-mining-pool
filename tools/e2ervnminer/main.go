// Command e2ervnminer is a minimal KawPow stratum miner (kawpowminer dialect)
// for end-to-end testing the kawpow engine against ravend regtest. It uses
// powkit's kawpow, the same implementation the pool verifies with.
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
	"strconv"
	"strings"
	"time"

	"github.com/sencha-dev/powkit/kawpow"
)

var (
	poolAddr = flag.String("pool", "127.0.0.1:3048", "stratum host:port")
	worker   = flag.String("worker", "miner", "worker name")
)

func main() {
	flag.Parse()
	conn, err := net.Dial("tcp", *poolAddr)
	must(err)
	defer conn.Close()
	br := bufio.NewReader(conn)

	send(conn, 1, "mining.subscribe", []interface{}{"e2e"})
	sub := readByID(br, 1)
	res := sub["result"].([]interface{})
	en1, _ := hex.DecodeString(res[1].(string)) // 2-byte nonce prefix
	send(conn, 2, "mining.authorize", []interface{}{*worker, "x"})

	var p []interface{}
	for {
		m := readLine(br)
		if m["method"] == "mining.notify" {
			p = m["params"].([]interface{})
			break
		}
	}
	jobID := p[0].(string)
	headerHash, _ := hex.DecodeString(strings.TrimPrefix(p[1].(string), "0x"))
	height := uint64(toF(p[5]))
	bits := strings.TrimPrefix(p[6].(string), "0x")
	// mine to the assigned share target (notify param 3) — as a real miner does.
	// It is <= the network target, so a share below it is also a valid block.
	target, _ := new(big.Int).SetString(strings.TrimPrefix(p[3].(string), "0x"), 16)
	if nt := bitsToTarget(bits); target == nil || target.Sign() == 0 || nt.Cmp(target) < 0 {
		target = nt
	}
	fmt.Printf("[rvn] job=%s height=%d headerHash=%s target=%x\n", jobID, height, p[1].(string)[:14], target)

	client := kawpow.NewRavencoin()
	fmt.Println("[rvn] searching (generates the epoch cache)...")
	start := time.Now()
	// high 2 bytes of the 64-bit nonce must be the assigned extranonce1
	base := uint64(binary.BigEndian.Uint16(en1)) << 48
	for i := uint64(0); ; i++ {
		nonce := base | i
		mix, digest, err := client.Compute(headerHash, height, nonce)
		must(err)
		if new(big.Int).SetBytes(digest).Cmp(target) <= 0 {
			fmt.Printf("[rvn] solution nonce=%016x after %d hashes in %s\n", nonce, i+1, time.Since(start))
			send(conn, 4, "mining.submit", []interface{}{
				*worker, jobID,
				fmt.Sprintf("0x%016x", nonce),
				"0x" + hex.EncodeToString(headerHash),
				"0x" + hex.EncodeToString(mix),
			})
			fmt.Printf("[rvn] submit response: %v\n", readByID(br, 4))
			return
		}
		if i > 2_000_000 {
			fmt.Println("[rvn] gave up")
			os.Exit(1)
		}
	}
}

func bitsToTarget(bitsHex string) *big.Int {
	b, _ := hex.DecodeString(bitsHex)
	exp := b[0]
	mant := new(big.Int).SetBytes(b[1:])
	return new(big.Int).Mul(mant, new(big.Int).Exp(big.NewInt(2), big.NewInt(8*int64(exp-3)), nil))
}

func toF(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
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
		fmt.Println("[rvn] error:", err)
		os.Exit(1)
	}
}
