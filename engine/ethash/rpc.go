package ethash

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ethRPC is a minimal Ethereum JSON-RPC 2.0 client. It is intentionally
// separate from the bitcoin daemons.DaemonManager: geth/core-geth require the
// "jsonrpc":"2.0" envelope and use eth_* methods, so the engine owns its own
// node transport (node interaction is the engine's responsibility).
type ethRPC struct {
	url    string
	client *http.Client
	id     int64
}

func newEthRPC(url string) *ethRPC {
	return &ethRPC{
		url:    url,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int64         `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

func (r *ethRPC) call(method string, params []interface{}, out interface{}) error {
	r.id++
	if params == nil {
		params = []interface{}{}
	}
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: r.id, Method: method, Params: params})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var rr rpcResponse
	if err := json.Unmarshal(raw, &rr); err != nil {
		return fmt.Errorf("decode %s response: %w (body: %s)", method, err, string(raw))
	}
	if rr.Error != nil {
		return rr.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(rr.Result, out)
}

// GetWork returns the current work package: [headerHash, seedHash, target] and,
// on nodes that provide it, a 4th element with the next block number (hex).
func (r *ethRPC) GetWork() (work []string, err error) {
	if err = r.call("eth_getWork", nil, &work); err != nil {
		return nil, err
	}
	if len(work) < 3 {
		return nil, errors.New("eth_getWork returned fewer than 3 fields")
	}
	return work, nil
}

// SubmitWork submits a solved nonce/mixHash for the given header hash. All
// arguments are 0x-prefixed hex. It returns whether the node accepted the block.
func (r *ethRPC) SubmitWork(nonce, headerHash, mixHash string) (bool, error) {
	var ok bool
	err := r.call("eth_submitWork", []interface{}{nonce, headerHash, mixHash}, &ok)
	return ok, err
}

// BlockNumber returns the current head block number (hex string like "0x10").
func (r *ethRPC) BlockNumber() (string, error) {
	var n string
	err := r.call("eth_blockNumber", nil, &n)
	return n, err
}
