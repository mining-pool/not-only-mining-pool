package config

import (
	"strings"

	"github.com/mining-pool/not-only-mining-pool/utils"
)

type Recipient struct {
	Address string  `json:"address"`
	Type    string  `json:"type"`
	Percent float64 `json:"percent"`

	script []byte
}

func (r *Recipient) GetScript() []byte {
	if r.script == nil {
		switch strings.ToLower(r.Type) {
		case "p2sh":
			r.script = utils.P2SHAddressToScript(r.Address)
		case "p2pkh":
			r.script = utils.P2PKHAddressToScript(r.Address)
		case "p2wsh":
			r.script = utils.P2WSHAddressToScript(r.Address)
		case "pk", "publickey":
			r.script = utils.PublicKeyToScript(r.Address)
		case "script":
			r.script = utils.ScriptPubKeyToScript(r.Address)
		case "":
			log.Error(r.Address, " has no type!")
		default:
			log.Error(r.Address, " uses an unsupported type: ", r.Type)

		}
	}

	return r.script
}

type PaymentOptions struct {
	Interval   int64   `json:"interval"`   // seconds between payout runs (default 600)
	MinPayment float64 `json:"minPayment"` // minimum coin owed before a miner is paid

	Daemon int `json:"daemon"` // index into daemons[] used for wallet RPC (default 0)

	// --- coin-fork configurability ---

	// Magnitude is the number of base units (satoshis) in one coin, e.g. 1e8 for
	// Bitcoin. 0 auto-detects from the wallet's getbalance precision.
	Magnitude float64 `json:"magnitude"`

	// MinConfirmations is coinbase maturity: a found block's reward is only paid
	// once its generation transaction has at least this many confirmations
	// (default 100; some forks use 30/40/240).
	MinConfirmations int64 `json:"minConfirmations"`

	// AddressCheckMethod is the wallet RPC used to verify the pool owns its
	// payout address: "getaddressinfo" (Bitcoin Core 0.18+, default) or
	// "validateaddress" (older cores / forks that kept it).
	AddressCheckMethod string `json:"addressCheckMethod"`

	// SendManyDummy is the leading dummy/account argument to sendmany. Bitcoin
	// Core requires it and keeps it as "" (the default).
	SendManyDummy string `json:"sendManyDummy"`
	// OmitSendManyDummy drops the leading argument entirely for forks whose
	// sendmany takes only the {address:amount} map.
	OmitSendManyDummy bool `json:"omitSendManyDummy"`
}

// WithDefaults returns a copy with unset fork knobs filled with the Bitcoin
// Core defaults, so a minimal config still works on mainstream forks.
func (o *PaymentOptions) WithDefaults() *PaymentOptions {
	c := *o
	if c.Interval <= 0 {
		c.Interval = 600
	}
	if c.MinConfirmations <= 0 {
		c.MinConfirmations = 100
	}
	if c.AddressCheckMethod == "" {
		c.AddressCheckMethod = "getaddressinfo"
	}
	// SendManyDummy defaults to "" (its zero value), which is what Bitcoin Core wants.
	return &c
}
