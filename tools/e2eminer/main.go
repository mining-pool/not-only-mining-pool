// Command e2eminer is a minimal sha256d stratum miner used for end-to-end
// testing against this pool on regtest. It subscribes, authorizes, reconstructs
// the block header exactly as jobs.Job.SerializeHeader does, brute-forces the
// nonce until the hash is below the network target, and submits the share. On
// regtest the target is trivial, so a block is found quickly.
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/mining-pool/not-only-mining-pool/algorithm"
	"github.com/mining-pool/not-only-mining-pool/utils"
)

var (
	poolAddr = flag.String("pool", "127.0.0.1:3032", "stratum host:port")
	worker   = flag.String("worker", "miner", "worker name")
	rpcURL   = flag.String("rpc", "http://rpcuser:rpcpassword@127.0.0.1:18443", "bitcoind rpc url")
	algoName = flag.String("algo", "sha256d", "header PoW algorithm (sha256d/scrypt/x11/groestl/keccak/...)")
	cbHash   = flag.String("coinbasehash", "sha256d", "coinbase/merkle hash (sha256d, or sha256 for groestlcoin)")
)

func main() {
	flag.Parse()

	prevHashDisplay, gbtTargetHex, gbtCurTime := templatePrevhashAndTarget()
	prevHash := utils.HexDecode([]byte(prevHashDisplay))
	var gbtTarget *big.Int
	if gbtTargetHex != "" {
		gbtTarget, _ = new(big.Int).SetString(gbtTargetHex, 16)
	}

	conn, err := net.Dial("tcp", *poolAddr)
	must(err)
	defer conn.Close()
	br := bufio.NewReader(conn)

	// subscribe
	send(conn, 1, "mining.subscribe", []interface{}{})
	subResp := readByID(br, 1)
	result := subResp["result"].([]interface{})
	extraNonce1, _ := hex.DecodeString(result[1].(string))
	en2size := int(result[2].(float64))
	fmt.Printf("[miner] extranonce1=%x extranonce2size=%d\n", extraNonce1, en2size)

	// authorize
	send(conn, 2, "mining.authorize", []interface{}{*worker, "x"})

	// wait for a mining.notify
	notify := waitNotify(br)
	jobID := notify[0].(string)
	coinb1 := utils.HexDecode([]byte(notify[2].(string)))
	coinb2 := utils.HexDecode([]byte(notify[3].(string)))
	var branch [][]byte
	for _, h := range notify[4].([]interface{}) {
		branch = append(branch, utils.HexDecode([]byte(h.(string))))
	}
	versionBE := utils.HexDecode([]byte(notify[5].(string)))
	bits := utils.HexDecode([]byte(notify[6].(string)))
	// nTime must land in the pool's accepted window [job.CurTime, submitTime+7].
	// now+2 sits above CurTime (which the harness has waited to be <= now+3...
	// actually <= now) and within the +7 bound.
	nt := uint32(time.Now().Unix()) + 2
	if gbtCurTime > nt {
		nt = gbtCurTime
	}
	ntimeBE := utils.PackUint32BE(nt)
	// mine to the hardest of (nBits target, GBT "target"), so the solution is
	// below whichever value the pool uses for its block check.
	target := utils.BigIntFromBitsBytes(bits)
	if gbtTarget != nil && gbtTarget.Sign() > 0 && gbtTarget.Cmp(target) < 0 {
		target = gbtTarget
	}
	fmt.Printf("[miner] job=%s branches=%d target=%x\n", jobID, len(branch), target)

	extraNonce2 := make([]byte, en2size) // fixed zeros; nonce space is enough on regtest

	// coinbase -> merkle root (internal order), folded with the coin's txid hash
	cbHasher := algorithm.GetHashFunc(*cbHash)
	coinbase := bytes.Join([][]byte{coinb1, extraNonce1, extraNonce2, coinb2}, nil)
	merkle := cbHasher(coinbase)
	for _, b := range branch {
		merkle = cbHasher(append(append([]byte{}, merkle...), b...))
	}
	merkleForHeader := utils.ReverseBytes(merkle) // SerializeHeader is handed the reversed root

	// brute force the nonce exactly like SerializeHeader + the pool's hash check
	powHash := algorithm.GetHashFunc(*algoName)
	start := time.Now()
	for nonce := uint32(0); ; nonce++ {
		header := serializeHeader(versionBE, prevHash, merkleForHeader, ntimeBE, bits, utils.PackUint32BE(nonce))
		h := powHash(header)
		if new(big.Int).SetBytes(utils.ReverseBytes(h)).Cmp(target) < 0 {
			nonceHex := hex.EncodeToString(utils.PackUint32BE(nonce))
			fmt.Printf("[miner] solution nonce=%s after %d tries in %s\n", nonceHex, nonce, time.Since(start))
			send(conn, 4, "mining.submit", []interface{}{
				*worker, jobID, hex.EncodeToString(extraNonce2),
				hex.EncodeToString(ntimeBE), nonceHex,
			})
			resp := readByID(br, 4)
			fmt.Printf("[miner] submit response: %v\n", resp)
			return
		}
		if nonce == 0xffffffff {
			fmt.Println("[miner] exhausted nonce space")
			os.Exit(1)
		}
	}
}

// serializeHeader mirrors jobs.Job.SerializeHeader: fields laid out in reverse
// order then the whole 80 bytes reversed.
func serializeHeader(versionBE, prevHash, merkleRoot, ntime, bits, nonce []byte) []byte {
	header := make([]byte, 80)
	pos := 0
	pos += copy(header[pos:], nonce)
	pos += copy(header[pos:], bits)
	pos += copy(header[pos:], ntime)
	pos += copy(header[pos:], merkleRoot)
	pos += copy(header[pos:], prevHash)
	binary.BigEndian.PutUint32(header[pos:], binary.BigEndian.Uint32(versionBE))
	return utils.ReverseBytes(header)
}

// --- stratum helpers ---

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

func waitNotify(br *bufio.Reader) []interface{} {
	for {
		m := readLine(br)
		if m["method"] == "mining.notify" {
			return m["params"].([]interface{})
		}
	}
}

func readLine(br *bufio.Reader) map[string]interface{} {
	line, err := br.ReadBytes('\n')
	must(err)
	var m map[string]interface{}
	must(json.Unmarshal(line, &m))
	return m
}

// --- rpc ---

func templatePrevhashAndTarget() (prevhash, target string, curtime uint32) {
	for _, rules := range []string{`["segwit"]`, `["mweb","segwit"]`, `[]`} {
		body := `{"jsonrpc":"1.0","id":"m","method":"getblocktemplate","params":[{"rules":` + rules + `}]}`
		req, _ := http.NewRequest("POST", *rpcURL, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "text/plain")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var r struct {
			Result *struct {
				PreviousBlockHash string `json:"previousblockhash"`
				Target            string `json:"target"`
				CurTime           uint32 `json:"curtime"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &r) == nil && r.Result != nil && r.Result.PreviousBlockHash != "" {
			return r.Result.PreviousBlockHash, r.Result.Target, r.Result.CurTime
		}
	}
	must(fmt.Errorf("getblocktemplate returned no usable result"))
	return "", "", 0
}

func must(err error) {
	if err != nil {
		fmt.Println("[miner] error:", err)
		os.Exit(1)
	}
}
