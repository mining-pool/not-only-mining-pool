package cryptonote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// xmrRPC talks to monerod's /json_rpc endpoint (JSON-RPC 2.0).
type xmrRPC struct {
	url    string
	client *http.Client
	id     int64
}

func newXmrRPC(baseURL string) *xmrRPC {
	return &xmrRPC{
		url:    strings.TrimRight(baseURL, "/") + "/json_rpc",
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

type blockTemplate struct {
	BlocktemplateBlob string `json:"blocktemplate_blob"`
	BlockhashingBlob  string `json:"blockhashing_blob"`
	Difficulty        uint64 `json:"difficulty"`
	Height            int64  `json:"height"`
	PrevHash          string `json:"prev_hash"`
	ReservedOffset    int    `json:"reserved_offset"`
	SeedHash          string `json:"seed_hash"`
	NextSeedHash      string `json:"next_seed_hash"`
	Status            string `json:"status"`
}

func (r *xmrRPC) call(method string, params interface{}, out interface{}) error {
	r.id++
	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": r.id, "method": method, "params": params,
	})
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

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode %s: %w (body %s)", method, err, raw)
	}
	if envelope.Error != nil {
		return fmt.Errorf("%s: rpc error %d: %s", method, envelope.Error.Code, envelope.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

// GetBlockTemplate asks monerod for a template paying walletAddress, with
// reserveSize bytes reserved in tx_extra for the pool's extranonce.
func (r *xmrRPC) GetBlockTemplate(walletAddress string, reserveSize int) (*blockTemplate, error) {
	var t blockTemplate
	err := r.call("get_block_template", map[string]interface{}{
		"wallet_address": walletAddress,
		"reserve_size":   reserveSize,
	}, &t)
	if err != nil {
		return nil, err
	}
	if t.Status != "OK" {
		return nil, fmt.Errorf("get_block_template status %q", t.Status)
	}
	return &t, nil
}

// SubmitBlock submits a full block blob (hex).
func (r *xmrRPC) SubmitBlock(blockHex string) error {
	var res struct {
		Status string `json:"status"`
	}
	if err := r.call("submit_block", []string{blockHex}, &res); err != nil {
		return err
	}
	if res.Status != "OK" {
		return fmt.Errorf("submit_block status %q", res.Status)
	}
	return nil
}
