package config

import (
	"github.com/mining-pool/go-pool-server/utils"
	"log"
	"strings"
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
			r.script = utils.P2SHAddressToScript(r.Address)
		case "p2wsh":
			r.script = utils.P2WSHAddressToScript(r.Address)
		case "pk", "publickey", "minerkey":
			r.script = utils.PublicKeyToScript(r.Address)
		case "":
			log.Fatal(r.Address + " has no type!")
		default:
			log.Fatal(r.Address + " uses an unsupported type: " + r.Type)

		}
	}

	return r.script
}
