package ethash

import (
	"encoding/json"
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTargetDifficultyRoundTrip(t *testing.T) {
	for _, diff := range []float64{1, 2, 1000, 1e6, 4.2e9} {
		target := TargetFromDifficulty(diff)
		got := DifficultyFromResult(target)
		// allow small float error
		if math.Abs(got-diff)/diff > 1e-6 {
			t.Errorf("roundtrip diff=%v -> target=%s -> diff=%v", diff, target.Text(16), got)
		}
	}
}

func TestMeetsTarget(t *testing.T) {
	target := TargetFromDifficulty(1000)
	// a result exactly at target passes; target+1 fails
	if !MeetsTarget(new(big.Int).Set(target), target) {
		t.Fatal("result == target should meet target")
	}
	over := new(big.Int).Add(target, big.NewInt(1))
	if MeetsTarget(over, target) {
		t.Fatal("result > target must not meet target")
	}
}

func TestParseSubmit(t *testing.T) {
	n, h, m, ok := parseSubmit([]interface{}{"0x1122334455667788", "0xdead", "0xbeef"})
	if !ok || n != "0x1122334455667788" || h != "0xdead" || m != "0xbeef" {
		t.Fatalf("parseSubmit bad: %v %v %v %v", n, h, m, ok)
	}
	if _, _, _, ok := parseSubmit([]interface{}{"only-one"}); ok {
		t.Fatal("parseSubmit should fail on too few params")
	}
	if _, _, _, ok := parseSubmit([]interface{}{1, 2, 3}); ok {
		t.Fatal("parseSubmit should fail on non-string params")
	}
}


// fakeNode is a minimal eth JSON-RPC node for exercising the RPC + RefreshWork
// plumbing without a real chain or etchash DAG.
func fakeNode(t *testing.T, getWork []string, acceptSubmit bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		var result interface{}
		switch req.Method {
		case "eth_getWork":
			result = getWork
		case "eth_submitWork":
			result = acceptSubmit
		case "eth_blockNumber":
			result = "0x10"
		default:
			t.Errorf("unexpected method %q", req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req.ID, "result": result,
		})
	}))
}

func TestRPCGetAndSubmit(t *testing.T) {
	srv := fakeNode(t, []string{"0xaa", "0xbb", "0x0f", "0x1234"}, true)
	defer srv.Close()

	rpc := newEthRPC(srv.URL)
	work, err := rpc.GetWork()
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != 4 || work[0] != "0xaa" || work[3] != "0x1234" {
		t.Fatalf("unexpected work: %v", work)
	}

	ok, err := rpc.SubmitWork("0x01", "0xaa", "0xcc")
	if err != nil || !ok {
		t.Fatalf("submitWork: ok=%v err=%v", ok, err)
	}
}

func TestRefreshWorkAndJobParams(t *testing.T) {
	// blockNumber comes from the 4th getWork field (0x1234 = 4660)
	srv := fakeNode(t, []string{"0xheaderhash", "0xseedhash", "0x0000ffff", "0x1234"}, true)
	defer srv.Close()

	e := New()
	e.rpc = newEthRPC(srv.URL)

	changed, err := e.RefreshWork()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first RefreshWork should report changed")
	}

	jobID, header, seed, num := e.CurrentWork()
	if header != "0xheaderhash" || seed != "0xseedhash" || num != 0x1234 || jobID == "" {
		t.Fatalf("CurrentWork wrong: %q %q %q %d", jobID, header, seed, num)
	}

	// re-fetch identical work -> not changed
	changed, err = e.RefreshWork()
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("identical work should not report changed")
	}

	params := e.JobParamsForDifficulty(1)
	if len(params) != 3 || params[0] != "0xheaderhash" {
		t.Fatalf("JobParams wrong: %v", params)
	}
	target, _ := params[2].(string)
	if len(target) < 3 || target[:2] != "0x" {
		t.Fatalf("target not 0x-hex: %v", params[2])
	}
}

// TestPoWVerify_NeedsLiveChain documents why full PoW verification is not
// exercised here: go-etchash light verification needs a correct
// (blockNumber, headerHash, nonce, mixHash) vector plus multi-second cache
// generation; validate it on a live ETC/core-geth private chain via the e2e
// harness instead.
func TestPoWVerify_NeedsLiveChain(t *testing.T) {
	t.Skip("etchash Compute needs a real header/nonce/mix vector + DAG cache; verify on a live chain")
}
