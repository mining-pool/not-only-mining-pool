package quantus

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/storage"
	"golang.org/x/crypto/blake2b"
)

// This ledger deliberately does not use the Bitcoin float64 balance tables.
// One watched Redis document makes candidate snapshots, credits, and debits
// atomic. Redis persistence (AOF) is required for production payments.
type quantusLedger struct {
	Genesis        string               `json:"genesis"`
	Sender         string               `json:"sender"`
	RewardAddress  string               `json:"rewardAddress"`
	Cursor         uint64               `json:"cursor"`
	Weights        map[string]string    `json:"weights"`
	SettledWeights map[string]string    `json:"settledWeights"`
	Balances       map[string]string    `json:"balances"`
	Candidates     map[string]candidate `json:"candidates"`
	SeenJob        string               `json:"seenJob"`
	Seen           map[string]bool      `json:"seen"`
	Intent         *paymentIntent       `json:"intent,omitempty"`
	Paid           []paymentReceipt     `json:"paid"`
	Blocks         []blockReceipt       `json:"blocks"`
	Fault          string               `json:"fault,omitempty"`
	Sequence       uint64               `json:"sequence"`
}
type candidate struct {
	Sequence uint64            `json:"sequence"`
	Finder   string            `json:"finder"`
	Weights  map[string]string `json:"weights"`
}
type paymentOutput struct {
	Address string `json:"address"`
	Amount  string `json:"amount"`
}
type paymentIntent struct {
	ID      string          `json:"id"`
	Outputs []paymentOutput `json:"outputs"`
	Raw     string          `json:"raw,omitempty"`
	Hash    string          `json:"hash,omitempty"`
	Nonce   string          `json:"nonce,omitempty"`
}
type paymentReceipt struct {
	Hash    string          `json:"hash"`
	Height  uint64          `json:"height"`
	Outputs []paymentOutput `json:"outputs"`
}
type blockReceipt struct {
	Hash   string `json:"hash"`
	Height uint64 `json:"height"`
	Reward string `json:"reward"`
}
type walletReply struct {
	Genesis            string        `json:"genesis"`
	Sender             string        `json:"sender"`
	Finalized          uint64        `json:"finalized"`
	Raw                string        `json:"raw"`
	Hash               string        `json:"hash"`
	Nonce              string        `json:"nonce"`
	ExistentialDeposit string        `json:"existentialDeposit"`
	Blocks             []walletBlock `json:"blocks"`
}
type walletBlock struct {
	Height  uint64      `json:"height"`
	Hash    string      `json:"hash"`
	Header  chainHeader `json:"header"`
	Rewards []struct {
		Miner  string `json:"miner"`
		Amount string `json:"amount"`
	} `json:"rewards"`
	Transactions []struct {
		Hash    string `json:"hash"`
		Success *bool  `json:"success"`
	} `json:"transactions"`
}
type quantusPayments struct {
	ctx        context.Context
	db         *redis.Client
	key        string
	q          *config.QuantusPaymentOptions
	mode       string
	interval   time.Duration
	minimum    *big.Int
	recipients []*config.Recipient
	call       func(string, map[string]interface{}) (*walletReply, error)
}

func units(s string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 || n.BitLen() > 128 || strings.Trim(s, "0123456789") != "" {
		return nil, errors.New("amount must be an unsigned decimal u128 string")
	}
	return n, nil
}
func integer(s string) *big.Int {
	if s == "" {
		return new(big.Int)
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("invalid persisted Quantus integer")
	}
	return n
}
func cloneWeights(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func percent(v float64) *big.Rat {
	r, _ := new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
	return r
}

func validatePayments(opts *config.Options) error {
	if opts.DisablePayment {
		return nil
	}
	if opts.PaymentOptions == nil || opts.Quantus.Payments == nil {
		return errors.New("quantus: paymentOptions and quantus.payments are required")
	}
	q := opts.Quantus.Payments
	if q.RPCURL == "" || q.WalletScript == "" || q.SeedFile == "" {
		return errors.New("quantus: payments require rpcUrl, walletScript and seedFile")
	}
	if _, err := hexBytes(q.GenesisHash, 32); err != nil {
		return fmt.Errorf("quantus: payments genesisHash: %w", err)
	}
	if a, err := payoutAddress(q.RewardAddress); err != nil || a != q.RewardAddress {
		return errors.New("quantus: rewardAddress must be a Quantus address without a worker suffix")
	}
	min, err := units(q.MinPaymentUnits)
	if err != nil || min.Sign() == 0 {
		return errors.New("quantus: minPaymentUnits must be positive")
	}
	reserve, err := units(q.ReserveUnits)
	if err != nil || reserve.Sign() == 0 {
		return errors.New("quantus: reserveUnits must be positive")
	}
	mode := opts.PaymentOptions.WithDefaults().PayMode
	if mode != "prop" && mode != "solo" {
		return errors.New("quantus: supported payment modes are prop and solo")
	}
	if opts.PaymentOptions.MinPayment != 0 {
		return errors.New("quantus: use exact quantus.payments.minPaymentUnits instead of minPayment")
	}
	total := new(big.Rat)
	for _, r := range opts.RewardRecipients {
		if r == nil {
			return errors.New("quantus: nil reward recipient")
		}
		if a, err := payoutAddress(r.Address); err != nil || a != r.Address {
			return errors.New("quantus: fee recipient must be a Quantus address without a worker suffix")
		}
		v := percent(r.Percent)
		if v == nil || v.Sign() < 0 {
			return errors.New("quantus: invalid fee percentage")
		}
		total.Add(total, v)
	}
	if total.Cmp(big.NewRat(100, 1)) >= 0 {
		return errors.New("quantus: total fees must be below 100 percent")
	}
	return nil
}

// InitPayments is an optional engine capability wired by pool.NewEnginePool.
func (e *Engine) InitPayments(opts *config.Options, db *storage.DB) error {
	if err := validatePayments(opts); err != nil {
		return err
	}
	// Every externally visible payment depends on the signed intent surviving
	// a Redis restart. Refuse volatile/default once-per-second persistence.
	for setting, expected := range map[string]string{"appendonly": "yes", "appendfsync": "always"} {
		ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
		values, err := db.ConfigGet(ctx, setting).Result()
		cancel()
		if err != nil {
			return fmt.Errorf("quantus: verify Redis %s: %w", setting, err)
		}
		if len(values) != 2 || values[1] != expected {
			return fmt.Errorf("quantus: payments require Redis %s=%s", setting, expected)
		}
	}
	p := &quantusPayments{ctx: e.ctx, db: db.Client, key: opts.Coin.Name + ":quantus:ledger", q: opts.Quantus.Payments,
		mode: opts.PaymentOptions.WithDefaults().PayMode, interval: time.Duration(opts.PaymentOptions.WithDefaults().Interval) * time.Second, recipients: opts.RewardRecipients}
	p.minimum, _ = units(p.q.MinPaymentUnits)
	p.call = p.wallet
	info, err := p.call("info", nil)
	if err != nil {
		return err
	}
	ed, err := units(info.ExistentialDeposit)
	if err != nil || p.minimum.Cmp(ed) < 0 {
		return errors.New("quantus: minPaymentUnits must be at least the chain existential deposit")
	}
	reserve, _ := units(p.q.ReserveUnits)
	if reserve.Cmp(ed) < 0 {
		return errors.New("quantus: reserveUnits must be at least the chain existential deposit")
	}
	for _, r := range p.recipients {
		if r.Address == info.Sender {
			return errors.New("quantus: fee recipient cannot be signing wallet")
		}
	}
	err = p.update(func(s *quantusLedger) error {
		if s.Genesis == "" {
			s.Genesis = info.Genesis
			s.Sender = info.Sender
			s.RewardAddress = p.q.RewardAddress
			s.Cursor = info.Finalized
		}
		if s.Genesis != info.Genesis || s.Genesis != p.q.GenesisHash || s.Sender != info.Sender || s.RewardAddress != p.q.RewardAddress {
			return errors.New("quantus: ledger chain/wallet/reward address mismatch")
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.payments = p
	e.payoutSender = info.Sender
	log.Warnf("Quantus payments enabled (%s), finalized height %d, signing wallet %s", p.mode, info.Finalized, info.Sender)
	return nil
}
func (e *Engine) ServePayments() {
	p := e.payments
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		if err := p.tick(); err != nil {
			log.Error("Quantus payments: ", err)
		}
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) PaymentStatus() (interface{}, error) {
	if e.payments == nil {
		return nil, errors.New("payments disabled")
	}
	s, err := e.payments.read()
	if err != nil {
		return nil, err
	}
	var pending interface{}
	if s.Intent != nil {
		pending = map[string]interface{}{"id": s.Intent.ID, "hash": s.Intent.Hash, "outputs": s.Intent.Outputs}
	}
	paid, blocks := s.Paid, s.Blocks
	if len(paid) > 100 {
		paid = paid[len(paid)-100:]
	}
	if len(blocks) > 100 {
		blocks = blocks[len(blocks)-100:]
	}
	return map[string]interface{}{"decimals": 12, "genesis": s.Genesis, "finalizedHeight": s.Cursor, "balances": s.Balances, "pending": pending, "paid": paid, "blocks": blocks, "unconfirmedCandidates": len(s.Candidates), "fault": s.Fault}, nil
}
func (p *quantusPayments) wallet(op string, fields map[string]interface{}) (*walletReply, error) {
	req := map[string]interface{}{"op": op, "rpc": p.q.RPCURL, "genesis": p.q.GenesisHash, "seedFile": p.q.SeedFile, "reserve": p.q.ReserveUnits}
	for k, v := range fields {
		req[k] = v
	}
	input, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(p.ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", p.q.WalletScript)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("wallet %s: %w: %s", op, err, strings.TrimSpace(stderr.String()))
	}
	var r walletReply
	if err = json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
func newLedger() *quantusLedger {
	return &quantusLedger{Weights: map[string]string{}, SettledWeights: map[string]string{}, Balances: map[string]string{}, Candidates: map[string]candidate{}, Seen: map[string]bool{}}
}
func (p *quantusPayments) update(fn func(*quantusLedger) error) error {
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	for attempt := 0; attempt < 32; attempt++ {
		err := p.db.Watch(ctx, func(tx *redis.Tx) error {
			s := newLedger()
			raw, err := tx.Get(ctx, p.key).Bytes()
			if err != nil && err != redis.Nil {
				return err
			}
			if err == nil {
				if err = json.Unmarshal(raw, s); err != nil {
					return err
				}
			}
			if err = fn(s); err != nil {
				return err
			}
			raw, err = json.Marshal(s)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error { pipe.Set(ctx, p.key, raw, 0); return nil })
			return err
		}, p.key)
		if err != redis.TxFailedErr {
			return err
		}
	}
	return errors.New("Quantus ledger busy")
}
func (p *quantusPayments) read() (*quantusLedger, error) {
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	b, err := p.db.Get(ctx, p.key).Bytes()
	if err != nil {
		return nil, err
	}
	s := newLedger()
	err = json.Unmarshal(b, s)
	return s, err
}

// record runs before a solution is sent upstream. A failed Redis write rejects
// the share; retrying an uncertain node write never credits it twice.
func (p *quantusPayments) record(worker, header, nonce string, difficulty *big.Int, isShare, isBlock bool) error {
	miner, err := payoutAddress(worker)
	if err != nil {
		return err
	}
	return p.update(func(s *quantusLedger) error {
		if miner == s.Sender {
			return errors.New("signing wallet cannot be a mining payout address")
		}
		if s.SeenJob != header {
			s.SeenJob = header
			s.Seen = map[string]bool{}
		}
		id := header + ":" + nonce
		if s.Seen[nonce] {
			return nil
		}
		if _, ok := s.Candidates[id]; ok {
			return nil
		}
		if isShare {
			s.Weights[miner] = new(big.Int).Add(integer(s.Weights[miner]), difficulty).String()
		}
		s.Sequence++
		if isBlock {
			s.Candidates[id] = candidate{Sequence: s.Sequence, Finder: miner, Weights: cloneWeights(s.Weights)}
		}
		s.Seen[nonce] = true
		return nil
	})
}

func (p *quantusPayments) settle(s *quantusLedger, b walletBlock) error {
	if b.Height != s.Cursor+1 {
		return errors.New("noncontiguous finalized blocks")
	}
	if s.Intent != nil && s.Intent.Hash != "" {
		for _, tx := range b.Transactions {
			if tx.Hash != s.Intent.Hash {
				continue
			}
			if tx.Success == nil {
				return errors.New("missing finalized extrinsic outcome")
			}
			if !*tx.Success {
				s.Fault = "finalized payment extrinsic failed: " + tx.Hash
				break
			}
			for _, o := range s.Intent.Outputs {
				n := new(big.Int).Sub(integer(s.Balances[o.Address]), integer(o.Amount))
				if n.Sign() < 0 {
					return errors.New("payment exceeds ledger balance")
				}
				s.Balances[o.Address] = n.String()
			}
			s.Paid = append(s.Paid, paymentReceipt{Hash: tx.Hash, Height: b.Height, Outputs: s.Intent.Outputs})
			s.Intent = nil
			break
		}
	}
	header, nonce, err := miningIdentity(b.Header)
	if err != nil {
		return err
	}
	id := header + ":" + nonce
	if c, ok := s.Candidates[id]; ok {
		reward := new(big.Int)
		for _, r := range b.Rewards {
			if r.Miner == p.q.RewardAddress {
				n, err := units(r.Amount)
				if err != nil {
					return err
				}
				reward.Add(reward, n)
			}
		}
		if reward.Sign() > 0 {
			remaining := new(big.Int).Set(reward)
			credit := func(a string, n *big.Int) { s.Balances[a] = new(big.Int).Add(integer(s.Balances[a]), n).String() }
			for _, r := range p.recipients {
				fraction := percent(r.Percent)
				fraction.Quo(fraction, big.NewRat(100, 1))
				amount := new(big.Int).Quo(new(big.Int).Mul(reward, fraction.Num()), fraction.Denom())
				credit(r.Address, amount)
				remaining.Sub(remaining, amount)
			}
			weights := map[string]*big.Int{}
			total := new(big.Int)
			for a, w := range c.Weights {
				d := new(big.Int).Sub(integer(w), integer(s.SettledWeights[a]))
				if d.Sign() < 0 {
					return errors.New("candidate share snapshot predates settled round")
				}
				if d.Sign() > 0 {
					weights[a] = d
					total.Add(total, d)
				}
			}
			if p.mode == "solo" || total.Sign() == 0 {
				credit(c.Finder, remaining)
			} else {
				left := new(big.Int).Set(remaining)
				for a, w := range weights {
					n := new(big.Int).Quo(new(big.Int).Mul(remaining, w), total)
					credit(a, n)
					left.Sub(left, n)
				}
				credit(c.Finder, left) // exact remainder, at most one base unit per participant
			}
			s.SettledWeights = cloneWeights(c.Weights)
			s.Blocks = append(s.Blocks, blockReceipt{Hash: b.Hash, Height: b.Height, Reward: reward.String()})
			delete(s.Candidates, id)
			// Earlier rejected siblings cannot later become canonical once this
			// block is finalized. Their shares remain in the settled round.
			if c.Sequence > 0 {
				for key, old := range s.Candidates {
					if old.Sequence <= c.Sequence {
						delete(s.Candidates, key)
					}
				}
			}
		}
	}
	s.Cursor = b.Height
	return nil
}

func (p *quantusPayments) tick() error {
	s, err := p.read()
	if err != nil {
		return err
	}
	if s.Fault != "" {
		return errors.New(s.Fault)
	}
	scan, err := p.call("scan", map[string]interface{}{"from": s.Cursor + 1})
	if err != nil {
		return err
	}
	if scan.Genesis != s.Genesis {
		return errors.New("chain genesis changed")
	}
	for _, b := range scan.Blocks {
		err = p.update(func(s *quantusLedger) error {
			if b.Height <= s.Cursor {
				return nil
			}
			return p.settle(s, b)
		})
		if err != nil {
			return err
		}
	}
	s, err = p.read()
	if err != nil {
		return err
	}
	if s.Fault != "" {
		return errors.New(s.Fault)
	}
	// Catch up before preparing or rebroadcasting to observe all finalized receipts.
	if s.Cursor < scan.Finalized {
		return nil
	}
	if s.Intent == nil {
		var id [16]byte
		if _, err = rand.Read(id[:]); err != nil {
			return err
		}
		err = p.update(func(s *quantusLedger) error {
			if s.Intent != nil {
				return nil
			}
			var addresses []string
			for a, v := range s.Balances {
				if integer(v).Cmp(p.minimum) >= 0 {
					addresses = append(addresses, a)
				}
			}
			sort.Strings(addresses)
			if len(addresses) > 64 {
				addresses = addresses[:64]
			}
			if len(addresses) == 0 {
				return nil
			}
			intent := &paymentIntent{ID: hex.EncodeToString(id[:])}
			for _, a := range addresses {
				intent.Outputs = append(intent.Outputs, paymentOutput{a, s.Balances[a]})
			}
			s.Intent = intent
			return nil
		})
		if err != nil {
			return err
		}
		s, err = p.read()
		if err != nil {
			return err
		}
	}
	if s.Intent == nil {
		return nil
	}
	id := s.Intent.ID
	if s.Intent.Raw == "" {
		r, err := p.call("prepare", map[string]interface{}{"sender": s.Sender, "outputs": s.Intent.Outputs})
		if err != nil {
			return err
		}
		if r.Genesis != s.Genesis || r.Sender != s.Sender || r.Raw == "" || r.Hash == "" {
			return errors.New("invalid signer reply")
		}
		raw, err := hex.DecodeString(strings.TrimPrefix(r.Raw, "0x"))
		if err != nil {
			return errors.New("invalid signed extrinsic encoding")
		}
		hash := blake2b.Sum256(raw)
		if r.Hash != "0x"+hex.EncodeToString(hash[:]) {
			return errors.New("signed extrinsic hash mismatch")
		}
		err = p.update(func(s *quantusLedger) error {
			if s.Intent != nil && s.Intent.ID == id && s.Intent.Raw == "" {
				s.Intent.Raw = r.Raw
				s.Intent.Hash = r.Hash
				s.Intent.Nonce = r.Nonce
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	s, err = p.read()
	if err != nil {
		return err
	}
	if s.Intent == nil || s.Intent.ID != id {
		return nil
	}
	// Only the exact signed bytes already committed to Redis may leave the process.
	r, err := p.call("broadcast", map[string]interface{}{"raw": s.Intent.Raw})
	if err != nil {
		return err
	}
	if r.Hash != s.Intent.Hash {
		return errors.New("broadcast returned unexpected transaction hash")
	}
	log.Warnf("Quantus payout broadcast %s; awaiting finalized ExtrinsicSuccess", r.Hash)
	return nil
}
