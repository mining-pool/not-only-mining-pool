package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mining-pool/not-only-mining-pool/config"
)

// Miner addresses are not published as a list: public clients may only look up
// an address they already know. Full indexes stay behind the admin session.

type adminContextKey struct{}

func withAdmin(r *http.Request, username string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), adminContextKey{}, username))
}

// isAdmin reports whether the request arrived through an authenticated /admin-api session.
func isAdmin(r *http.Request) bool {
	_, ok := r.Context().Value(adminContextKey{}).(string)
	return ok
}

// adminUser is the signed-in administrator, or "" outside an admin session.
func adminUser(r *http.Request) string {
	v, _ := r.Context().Value(adminContextKey{}).(string)
	return v
}

// view hides the miner index from public responses but keeps its size.
func (p PoolInfo) view(admin bool) PoolInfo {
	p.MinerCount = len(p.Miners)
	if !admin {
		p.Miners = nil
	}
	return p
}

type paymentOutput struct {
	Address string `json:"address"`
	Amount  string `json:"amount"`
}

type paymentPending struct {
	ID      string          `json:"id"`
	Hash    string          `json:"hash,omitempty"`
	Outputs []paymentOutput `json:"outputs"`
}

type paymentReceipt struct {
	Hash    string          `json:"hash"`
	Height  uint64          `json:"height"`
	Outputs []paymentOutput `json:"outputs"`
}

// paymentStatus is the engine-neutral payments contract served at /payments.
type paymentStatus struct {
	Decimals              int               `json:"decimals"`
	Genesis               string            `json:"genesis,omitempty"`
	FinalizedHeight       uint64            `json:"finalizedHeight"`
	Balances              map[string]string `json:"balances"`
	Pending               *paymentPending   `json:"pending"`
	Paid                  []paymentReceipt  `json:"paid"`
	Blocks                []json.RawMessage `json:"blocks"`
	UnconfirmedCandidates int               `json:"unconfirmedCandidates"`
	Fault                 string            `json:"fault,omitempty"`
}

func outputsFor(outputs []paymentOutput, address string) []paymentOutput {
	mine := make([]paymentOutput, 0)
	for _, o := range outputs {
		if o.Address == address {
			mine = append(mine, o)
		}
	}
	return mine
}

// view returns everything to administrators. Public callers get pool-level
// data (blocks, heights) plus only the records of the exact address they ask
// for; other miners' addresses, balances and the internal fault text are hidden.
func (s paymentStatus) view(admin bool, address string) paymentStatus {
	if admin {
		return s
	}
	public := paymentStatus{
		Decimals:              s.Decimals,
		Genesis:               s.Genesis,
		FinalizedHeight:       s.FinalizedHeight,
		Balances:              map[string]string{},
		Paid:                  []paymentReceipt{},
		Blocks:                s.Blocks,
		UnconfirmedCandidates: s.UnconfirmedCandidates,
	}
	if s.Fault != "" {
		public.Fault = "payment processing is delayed"
	}
	if address == "" {
		if s.Pending != nil {
			public.Pending = &paymentPending{ID: s.Pending.ID, Hash: s.Pending.Hash, Outputs: []paymentOutput{}}
		}
		return public
	}
	if balance, ok := s.Balances[address]; ok {
		public.Balances[address] = balance
	}
	if s.Pending != nil {
		if mine := outputsFor(s.Pending.Outputs, address); len(mine) > 0 {
			public.Pending = &paymentPending{ID: s.Pending.ID, Hash: s.Pending.Hash, Outputs: mine}
		}
	}
	for _, receipt := range s.Paid {
		if mine := outputsFor(receipt.Outputs, address); len(mine) > 0 {
			public.Paid = append(public.Paid, paymentReceipt{Hash: receipt.Hash, Height: receipt.Height, Outputs: mine})
		}
	}
	return public
}

// publicPort is what miners need to connect. TLS certificate and key paths are
// server internals and are reduced to a flag.
type publicPort struct {
	Protocol string                 `json:"protocol,omitempty"`
	Diff     float64                `json:"diff"`
	VarDiff  *config.VarDiffOptions `json:"varDiff"`
	TLS      bool                   `json:"tls"`
}

func publicPorts(ports map[int]*config.PortOptions) map[int]publicPort {
	out := make(map[int]publicPort, len(ports))
	for port, p := range ports {
		if p == nil {
			continue
		}
		out[port] = publicPort{Protocol: p.Protocol, Diff: p.Diff, VarDiff: p.VarDiff, TLS: p.TLS != nil}
	}
	return out
}
