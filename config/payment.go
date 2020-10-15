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
