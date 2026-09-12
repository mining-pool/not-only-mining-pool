package quantus

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/mining-pool/not-only-mining-pool/config"
	"golang.org/x/crypto/blake2b"
)

const testMiner = "qzntBpmqHZF1jxC8KJKpuxcYuHST892jyXBqRctpAxd1WQ9BL"
const testOther = "qzmAAFv4c7tprk5UyJfav4hgkGR8xyckGeZL2KhwM1FWMW1gk"
const testReward = "qzjccbu7hNZ4JGzkhTp2NGKeSyhueBV5ixD1PdEyZawF8XPZ9"

func devBlock(t *testing.T) walletBlock {
	t.Helper()
	data, err := os.ReadFile("testdata/dev-block-1.json")
	if err != nil {
		t.Fatal(err)
	}
	var b walletBlock
	if err = json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	b.Height = 1
	return b
}
func testPayments(t *testing.T) *quantusPayments {
	t.Helper()
	r := miniredis.RunT(t)
	db := redis.NewClient(&redis.Options{Addr: r.Addr()})
	t.Cleanup(func() { db.Close() })
	p := &quantusPayments{ctx: context.Background(), db: db, key: "test:quantus:ledger", q: &config.QuantusPaymentOptions{RewardAddress: testReward}, mode: "prop", minimum: big.NewInt(1)}
	if err := p.update(func(s *quantusLedger) error { s.Genesis = "genesis"; s.Sender = "sender"; return nil }); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestQuantusHeaderFromNode(t *testing.T) {
	b := devBlock(t)
	header, nonce, err := miningIdentity(b.Header)
	if err != nil {
		t.Fatal(err)
	}
	// Independently logged by quantus-node 1.0.1 before mining block #1.
	if header != "12dbbf3a2df7169359cba7c8014d58a2c7bb7bed8229b96771589e4164dda7de" {
		t.Fatal(header)
	}
	if nonce != "f35da0e84b2a3d737eb864ee26320b46518903de93f3d7ff0f8b1c3ff3232dbb785e66a569089dfac54738c13cf92ca7e0ca39b9c16b542f9de0e0d42232fac2" {
		t.Fatal(nonce)
	}
	b.Header.ZKTreeRoot = "0x" + strings.Repeat("00", 32)
	changed, _, _ := miningIdentity(b.Header)
	if changed == header {
		t.Fatal("zk root not committed")
	}
	b.Header.Digest.Logs = b.Header.Digest.Logs[:1]
	if _, _, err = miningIdentity(b.Header); err == nil {
		t.Fatal("accepted unsealed block")
	}
}
func TestQuantusAmountsAndAddresses(t *testing.T) {
	for _, a := range []string{testMiner, testOther, testReward} {
		got, err := payoutAddress(a + ".rig")
		if err != nil || got != a {
			t.Fatalf("%s: %v", a, err)
		}
	}
	for _, a := range []string{"account.rig", testMiner[:len(testMiner)-1] + "1", "0x" + strings.Repeat("00", 32)} {
		if _, err := payoutAddress(a); err == nil {
			t.Fatalf("accepted %q", a)
		}
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	if _, err := units(max.String()); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"", "-1", "1.1", "1e12", "+1", new(big.Int).Add(max, big.NewInt(1)).String()} {
		if _, err := units(v); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}
func TestQuantusSettlementExactAndOrphans(t *testing.T) {
	p := testPayments(t)
	b := devBlock(t)
	h, n, _ := miningIdentity(b.Header)
	reward := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 100), big.NewInt(17))
	b.Rewards = append(b.Rewards, struct {
		Miner  string `json:"miner"`
		Amount string `json:"amount"`
	}{testReward, reward.String()})
	s := newLedger()
	s.Candidates[h+":"+n] = candidate{Sequence: 2, Finder: testMiner, Weights: map[string]string{testMiner: "1", testOther: "2"}}
	s.Candidates["orphan"] = candidate{Sequence: 1, Finder: testOther, Weights: map[string]string{testOther: "1"}}
	s.Candidates["future"] = candidate{Sequence: 3, Finder: testOther, Weights: map[string]string{testOther: "9"}}
	p.recipients = []*config.Recipient{{Address: testReward, Percent: 0.1}}
	if err := p.settle(s, b); err != nil {
		t.Fatal(err)
	}
	fee := new(big.Int).Quo(new(big.Int).Set(reward), big.NewInt(1000))
	if s.Balances[testReward] != fee.String() {
		t.Fatal("inexact fee", s.Balances)
	}
	sum := new(big.Int)
	for _, v := range s.Balances {
		sum.Add(sum, integer(v))
	}
	if sum.Cmp(reward) != 0 {
		t.Fatal("lost rounding remainder")
	}
	if len(s.Candidates) != 1 || s.Candidates["future"].Sequence != 3 {
		t.Fatal("incorrect orphan pruning")
	}
	if len(s.Blocks) != 1 || s.SettledWeights[testOther] != "2" {
		t.Fatal(s)
	}
	// A different nonce and an unrelated reward event never create credit.
	other := newLedger()
	other.Candidates["unmatched"] = candidate{Finder: testOther}
	if err := p.settle(other, b); err != nil {
		t.Fatal(err)
	}
	if len(other.Balances) != 0 {
		t.Fatal("credited unrelated block")
	}
	solo := newLedger()
	solo.Candidates[h+":"+n] = candidate{Finder: testMiner, Weights: map[string]string{testOther: "99"}}
	p.mode = "solo"
	p.recipients = nil
	if err := p.settle(solo, b); err != nil {
		t.Fatal(err)
	}
	if solo.Balances[testMiner] != reward.String() || len(solo.Balances) != 1 {
		t.Fatal("incorrect solo credit")
	}
}
func TestQuantusDurableShareRetry(t *testing.T) {
	p := testPayments(t)
	for i := 0; i < 2; i++ {
		if err := p.record(testMiner+".rig", "header", "nonce", big.NewInt(13), true, true); err != nil {
			t.Fatal(err)
		}
	}
	s, err := p.read()
	if err != nil {
		t.Fatal(err)
	}
	if s.Weights[testMiner] != "13" || len(s.Candidates) != 1 {
		t.Fatal("duplicate accounting", s)
	}
	// A restart/new job cannot recredit a persisted candidate.
	if err = p.record(testOther, "other", "nonce2", big.NewInt(7), true, false); err != nil {
		t.Fatal(err)
	}
	if err = p.record(testMiner, "header", "nonce", big.NewInt(13), true, true); err != nil {
		t.Fatal(err)
	}
	s, _ = p.read()
	if s.Weights[testMiner] != "13" {
		t.Fatal("recredited old candidate")
	}
	p.db.Close()
	if err = p.record(testMiner, "h", "n", big.NewInt(1), true, true); err == nil {
		t.Fatal("accepted share with unavailable storage")
	}
}

func TestQuantusInsufficientFundsKeepsUnsignedIntent(t *testing.T) {
	p := testPayments(t)
	if err := p.update(func(s *quantusLedger) error { s.Balances[testMiner] = "1230000000000"; return nil }); err != nil {
		t.Fatal(err)
	}
	p.call = func(op string, fields map[string]interface{}) (*walletReply, error) {
		switch op {
		case "scan":
			return &walletReply{Genesis: "genesis"}, nil
		case "prepare":
			return nil, errors.New("Insufficient hot-wallet balance including transaction fee and reserve")
		default:
			t.Fatal("broadcast with insufficient funds")
			return nil, nil
		}
	}
	if err := p.tick(); err == nil {
		t.Fatal("missing insufficient funds error")
	}
	s, _ := p.read()
	if s.Intent == nil || s.Intent.Raw != "" || s.Balances[testMiner] != "1230000000000" || len(s.Paid) != 0 {
		t.Fatal("changed unpaid balance", s)
	}
	e := New()
	defer e.Close()
	e.payments = p
	s.Intent.Raw = "private signed bytes"
	if err := p.update(func(state *quantusLedger) error { state.Intent = s.Intent; return nil }); err != nil {
		t.Fatal(err)
	}
	status, err := e.PaymentStatus()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "private signed bytes") || strings.Contains(string(raw), `"raw"`) {
		t.Fatal("payment status leaked signed bytes")
	}
}
func TestQuantusPayoutResumeAndFinalizedFailure(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[success], func(t *testing.T) {
			p := testPayments(t)
			amount := "900719925474099312345"
			if err := p.update(func(s *quantusLedger) error { s.Balances[testMiner] = amount; return nil }); err != nil {
				t.Fatal(err)
			}
			raw := "0x010203"
			digest := blake2b.Sum256([]byte{1, 2, 3})
			hash := "0x" + hex.EncodeToString(digest[:])
			prepared, broadcast := 0, 0
			finalized := false
			p.call = func(op string, fields map[string]interface{}) (*walletReply, error) {
				switch op {
				case "scan":
					r := &walletReply{Genesis: "genesis"}
					if finalized {
						r.Finalized = 1
						if fields["from"].(uint64) == 1 {
							b := devBlock(t)
							b.Transactions = append(b.Transactions, struct {
								Hash    string `json:"hash"`
								Success *bool  `json:"success"`
							}{hash, &success})
							r.Blocks = []walletBlock{b}
						}
					}
					return r, nil
				case "prepare":
					prepared++
					return &walletReply{Genesis: "genesis", Sender: "sender", Raw: raw, Hash: hash, Nonce: "0"}, nil
				case "broadcast":
					broadcast++
					s, err := p.read()
					if err != nil {
						t.Fatal(err)
					}
					if s.Intent == nil || s.Intent.Raw != raw || fields["raw"] != raw {
						t.Fatal("broadcast before durable signing")
					}
					return nil, errors.New("simulated lost response after acceptance")
				}
				t.Fatal(op)
				return nil, nil
			}
			if err := p.tick(); err == nil {
				t.Fatal("missing broadcast failure")
			}
			// Recreate the manager while retaining only Redis state.
			restarted := *p
			p = &restarted
			if err := p.tick(); err == nil {
				t.Fatal("missing retry error")
			}
			if prepared != 1 || broadcast != 2 {
				t.Fatal("signed a replacement", prepared, broadcast)
			}
			finalized = true
			err := p.tick()
			if success && err != nil {
				t.Fatal(err)
			}
			if !success && err == nil {
				t.Fatal("failed extrinsic did not halt payments")
			}
			s, _ := p.read()
			if success {
				if s.Balances[testMiner] != "0" || len(s.Paid) != 1 || s.Intent != nil {
					t.Fatal("bad finalized debit", s)
				}
				if err = p.tick(); err != nil {
					t.Fatal(err)
				}
				s, _ = p.read()
				if len(s.Paid) != 1 {
					t.Fatal("duplicate finalized debit")
				}
			} else {
				if s.Balances[testMiner] != amount || len(s.Paid) != 0 || s.Fault == "" {
					t.Fatal("debited failed payout")
				}
			}
		})
	}
}
