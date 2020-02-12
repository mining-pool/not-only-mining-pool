package daemonManager

import (
	"encoding/json"
	"fmt"
	"log"
)

// TODO

type GetMiningInfo struct {
	Blocks           int     `json:"blocks"`
	Currentblocksize int     `json:"currentblocksize"`
	Currentblocktx   int     `json:"currentblocktx"`
	Difficulty       float64 `json:"difficulty"`
	Errors           string  `json:"errors"`
	Networkhashps    float64 `json:"networkhashps"`
	Pooledtx         int     `json:"pooledtx"`
	Chain            string  `json:"chain"`
}

func BytesToGetMiningInfo(b []byte) *GetMiningInfo {
	var getMiningInfo GetMiningInfo
	err := json.Unmarshal(b, &getMiningInfo)
	if err != nil {
		log.Fatal(fmt.Sprint("getDifficulty call failed with error ", err))
	}

	return &getMiningInfo
}
