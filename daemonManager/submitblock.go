package daemonManager

import (
	"encoding/json"
	"github.com/node-standalone-pool/go-pool-server/utils"
	"log"
)

// submitblock has no result
func (dm *DaemonManager) SubmitBlock(blockHex string) {
	log.Println("submitting block: " + blockHex)

	var results []*JsonRpcResponse
	if dm.Coin.NoSubmitMethod {
		_, results = dm.CmdAll("getblocktemplate", []interface{}{map[string]interface{}{"mode": "submit", "data": blockHex}})
	} else {
		_, results = dm.CmdAll("submitblock", []interface{}{blockHex})
	}

	for i := range results {
		if results[i].Error != nil {
			log.Fatal("rpc error with daemon when submitting block: " + string(utils.Jsonify(results[i].Error)))
		} else {
			var result string
			err := json.Unmarshal(results[i].Result, &result)
			if err == nil && result == "rejected" {
				log.Println("Daemon instance rejected a supposedly valid block")
			}
		}

		log.Println(string(utils.Jsonify(results[i].Result)))
	}
}
