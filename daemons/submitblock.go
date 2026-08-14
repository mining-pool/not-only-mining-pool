package daemons

import (
	"encoding/json"

	"github.com/mining-pool/not-only-mining-pool/utils"
)

// submitblock has no result
func (dm *DaemonManager) SubmitBlock(blockHex string) {
	var results []*JsonRpcResponse
	if dm.Coin.NoSubmitBlock {
		_, results = dm.CmdAll("getblocktemplate", []interface{}{map[string]interface{}{"mode": "submit", "data": blockHex}})
	} else {
		_, results = dm.CmdAll("submitblock", []interface{}{blockHex})
	}

	for i := range results {
		if results[i] == nil {
			log.Errorf("failed submitting to daemon %s, see log above for details", dm.Daemons[i].String())
			continue
		}

		if results[i].Error != nil {
			log.Error("rpc error with daemon when submitting block: " + string(utils.Jsonify(results[i].Error)))
		} else {
			var result string
			err := json.Unmarshal(results[i].Result, &result)
			// submitblock returns null on success, otherwise a reject reason
			// string (e.g. "high-hash", "bad-txnmrklroot", "duplicate").
			if err == nil && result != "" {
				log.Error("Daemon rejected the block: " + result)
			}
		}
	}
}
