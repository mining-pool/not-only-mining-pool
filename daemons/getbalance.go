package daemons

import (
	"encoding/json"
	"fmt"
)

type GetBalance float64

func BytesToGetBalance(b []byte) (GetBalance, error) {
	var getBalance GetBalance
	err := json.Unmarshal(b, &getBalance)
	if err != nil {
		return 0.0, fmt.Errorf("unmashal getBalance response %s failed with error %s", b, err)
	}

	return getBalance, nil
}
