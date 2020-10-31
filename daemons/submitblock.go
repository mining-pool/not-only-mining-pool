package daemons

import (
	"encoding/json"

	"github.com/mining-pool/not-only-mining-pool/utils"
)

// submitblock has no result
func (dm *DaemonManager) SubmitBlock(blockHex string) {
	var results []*JsonRpcResponse
	if dm.Coin.NoSubmitBlock {
		results, _ = dm.CmdAll("getblocktemplate", []interface{}{map[string]interface{}{"mode": "submit", "data": blockHex}})
	} else {
		results, _ = dm.CmdAll("submitblock", []interface{}{blockHex})
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
			if err == nil && result == "rejected" {
				log.Warn("Daemon instance rejected a supposedly valid block")
			}

			if err == nil && result == "invalid" {
				log.Warn("Daemon instance rejected an invalid block")
			}

			if err == nil && result == "inconclusive" {
				log.Warn("Daemon instance warns an inconclusive block")
			}
		}
	}
}
