package daemonManager

import (
	"encoding/json"
	"fmt"
	"log"
)

//// TODO

//type GetDifficulty interface {}

func BytesToGetDifficulty(b []byte) interface{} {
	var getDifficulty interface{}
	err := json.Unmarshal(b, &getDifficulty)
	if err != nil {
		log.Fatal(fmt.Sprint("getDifficulty call failed with error ", err))
	}

	return getDifficulty
}
