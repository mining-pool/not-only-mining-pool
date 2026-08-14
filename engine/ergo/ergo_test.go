package ergo

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mining-pool/not-only-mining-pool/engine"

	"github.com/mining-pool/not-only-mining-pool/types"
)

// TestAutolykos2ErgoVector pins the PoW to powkit's official Ergo vector, and
// checks the engine's target comparison logic around it.
func TestAutolykos2ErgoVector(t *testing.T) {
	msg, _ := hex.DecodeString("548c3e602a8f36f8f2738f5f643b02425038044d98543a51cabaa9785e7e864f")
	result, err := New().pow.Compute(msg, 614400, 0x3105)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(result) != "0002fcb113fe65e5754959872dfdbffea0489bf830beb4961ddc0e9e66a1412a" {
		t.Fatalf("ergo autolykos2 vector mismatch: %x", result)
	}
}

func TestShareTargetMath(t *testing.T) {
	if engine.TargetFromDiff(engine.Pow256, 2).Cmp(engine.TargetFromDiff(engine.Pow256, 1)) >= 0 {
		t.Fatal("higher diff must shrink the target")
	}
	if len(engine.TargetHex(big.NewInt(255))) != 64 {
		t.Fatal("targetHex must be 64 chars")
	}
}

type fakeSession struct {
	en1  []byte
	diff float64
}

func (f fakeSession) ExtraNonce1() []byte { return f.en1 }
func (f fakeSession) Difficulty() float64 { return f.diff }
func (f fakeSession) WorkerName() string  { return "w" }
func (f fakeSession) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}
func (f fakeSession) Send(string, []interface{}) error { return nil }

// TestOnSubmitWithVector drives OnSubmit with the real Ergo vector so a genuine
// autolykos2 result is accepted, while forged/duplicate/short nonces are not.
func TestOnSubmitWithVector(t *testing.T) {
	msg, _ := hex.DecodeString("548c3e602a8f36f8f2738f5f643b02425038044d98543a51cabaa9785e7e864f")
	e := New()
	j := &job{
		id:     "1",
		msg:    msg,
		height: 614400,
		target: big.NewInt(1), // unreachable -> never posts a solution (rest is nil)
		seen:   map[string]struct{}{},
	}
	e.cur = j
	e.history["1"] = j

	// nonce 0x3105 -> the vector, padded to 16 hex chars (8-byte nonce)
	nonceHex := "0000000000003105"
	sess := fakeSession{diff: 1e-30}

	share := e.OnSubmit(sess, []interface{}{"w", "1", nonceHex})
	if share.ErrorCode != 0 {
		t.Fatalf("known-good vector rejected: %s", share.ErrorCode.String())
	}
	if share.Diff <= 0 {
		t.Fatal("share diff must be positive")
	}

	if e.OnSubmit(sess, []interface{}{"w", "1", nonceHex}).ErrorCode != types.ErrDuplicateShare {
		t.Fatal("duplicate must be rejected")
	}
	if e.OnSubmit(sess, []interface{}{"w", "1", "0xshort"}).ErrorCode != types.ErrIncorrectNonceSize {
		t.Fatal("short nonce must be rejected")
	}
	if e.OnSubmit(sess, []interface{}{"w", "99", nonceHex}).ErrorCode != types.ErrJobNotFound {
		t.Fatal("unknown job must be rejected")
	}
}

// TestRESTCandidateParsing verifies the REST client copes with Ergo's large
// numeric target `b` (exceeds int64) via json.Number.
func TestRESTCandidateParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mining/candidate" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"msg": "548c3e602a8f36f8f2738f5f643b02425038044d98543a51cabaa9785e7e864f",
			"b":   json.RawMessage("115792089237316195423570985008687907853269984665640564039457584007913129639936"),
			"pk":  "0350e25cee85...",
			"h":   999999,
		})
	}))
	defer srv.Close()

	c, err := newErgoREST(srv.URL, "apikey").Candidate()
	if err != nil {
		t.Fatal(err)
	}
	if c.H != 999999 || len(c.Msg) != 64 {
		t.Fatalf("candidate parse wrong: %+v", c)
	}
	if _, ok := new(big.Int).SetString(c.B, 10); !ok {
		t.Fatalf("big target b did not survive parsing: %q", c.B)
	}
}
