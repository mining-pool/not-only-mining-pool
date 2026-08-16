package daemons

import (
	"encoding/json"
	"fmt"
)

type SendMany string // receive the raw tx hex string

func BytesToSendMany(b []byte) (*SendMany, error) {
	var sendMany SendMany
	err := json.Unmarshal(b, &sendMany)
	if err != nil {
		return nil, fmt.Errorf("getTransaction call failed with error %s", err)
	}

	return &sendMany, nil
}
