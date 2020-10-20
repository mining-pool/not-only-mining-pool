package daemons

import (
	"encoding/json"
	"fmt"
)

type GetNetworkInfo struct {
	Version         int    `json:"version"`
	Subversion      string `json:"subversion"`
	Protocolversion int    `json:"protocolversion"`
	Localservices   string `json:"localservices"`
	Localrelay      bool   `json:"localrelay"`
	Timeoffset      int    `json:"timeoffset"`
	Networkactive   bool   `json:"networkactive"`
	Connections     int    `json:"connections"`
	Networks        []struct {
		Name                      string `json:"name"`
		Limited                   bool   `json:"limited"`
		Reachable                 bool   `json:"reachable"`
		Proxy                     string `json:"proxy"`
		ProxyRandomizeCredentials bool   `json:"proxy_randomize_credentials"`
	} `json:"networks"`
	Relayfee       float64       `json:"relayfee"`
	Incrementalfee float64       `json:"incrementalfee"`
	Localaddresses []interface{} `json:"localaddresses"`
	Warnings       string        `json:"warnings"`
}

func BytesToGetNetworkInfo(b []byte) *GetNetworkInfo {
	var getNetworkInfo GetNetworkInfo
	err := json.Unmarshal(b, &getNetworkInfo)
	if err != nil {
		log.Fatal(fmt.Sprint("getDifficulty call failed with error ", err))
	}

	return &getNetworkInfo
}
