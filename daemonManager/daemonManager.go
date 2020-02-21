package daemonManager

import (
	"bytes"
	"encoding/json"
	"github.com/mining-pool/go-pool-server/config"
	"github.com/mining-pool/go-pool-server/utils"
	"log"
	"net/http"
)

type DaemonManager struct {
	Daemons []*config.DaemonOptions
	Coin    *config.CoinOptions
}

func NewDaemonManager(daemons []*config.DaemonOptions, coin *config.CoinOptions) *DaemonManager {
	if daemons == nil || coin == nil {
		log.Fatal("new daemon with empty options!")
	}

	return &DaemonManager{
		Daemons: daemons,
		Coin:    coin,
	}
}

func (dm *DaemonManager) Init() {
	online := dm.IsAllOnline()

	if online {
		log.Println("all online now")
	}
}

func (dm *DaemonManager) IsAllOnline() bool {
	results, _ := dm.CmdAll("getpeerinfo", []interface{}{})
	for _, res := range results {
		if res.StatusCode/100 != 2 {
			return false
		}

		var jsonRes JsonRpcResponse
		err := json.NewDecoder(res.Body).Decode(&jsonRes)
		if err != nil {
			return false
		}

		if jsonRes.Error != nil {
			return false
		}

	}

	return true
}

func (dm *DaemonManager) DoHttpRequest(daemon *config.DaemonOptions, reqRawData []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", daemon.URL(), bytes.NewReader(reqRawData))
	if err != nil {
		log.Fatal(err)
	}
	req.SetBasicAuth(daemon.User, daemon.Password)
	client := &http.Client{}
	return client.Do(req)
}

func (dm *DaemonManager) BatchCmd(commands []interface{}) (*config.DaemonOptions, []*JsonRpcResponse, error) {
	requestJson := make([]map[string]interface{}, len(commands))
	for i := range commands {
		requestJson[i] = map[string]interface{}{
			"id":     utils.RandPositiveInt64(),
			"method": commands[i].([]interface{})[0],
			"params": commands[i].([]interface{})[1],
		}
	}

	for i := range dm.Daemons {
		raw, _ := json.Marshal(requestJson)
		res, err := dm.DoHttpRequest(dm.Daemons[i], raw)
		if err != nil {
			return dm.Daemons[i], nil, err
		}
		var rpcResponses []*JsonRpcResponse
		err = json.NewDecoder(res.Body).Decode(&rpcResponses)
		if err != nil {
			return dm.Daemons[i], nil, err
		}

		return dm.Daemons[i], rpcResponses, err
	}

	return nil, nil, nil
}

func (dm *DaemonManager) CmdAll(method string, params []interface{}) ([]*http.Response, []*JsonRpcResponse) {
	responses := make([]*http.Response, 0)
	results := make([]*JsonRpcResponse, 0)
	for _, daemon := range dm.Daemons {
		reqRawData, err := json.Marshal(map[string]interface{}{
			"id":     utils.RandPositiveInt64(),
			"method": method,
			"params": params,
		})
		if err != nil {
			log.Fatal(err)
		}

		res, err := dm.DoHttpRequest(daemon, reqRawData)
		if err != nil {
			log.Fatal(err)
		}

		responses = append(responses, res)

		var result JsonRpcResponse
		err = json.NewDecoder(res.Body).Decode(&result)
		if err != nil {
			log.Fatal(err)
		}
		results = append(results, &result)
	}

	return responses, results
}

func (dm *DaemonManager) Cmd(method string, params []interface{}) (*http.Response, *JsonRpcResponse, *config.DaemonOptions) {
	for i := range dm.Daemons {
		reqRawData, err := json.Marshal(map[string]interface{}{
			"id":     utils.RandPositiveInt64(),
			"method": method,
			"params": params,
		})
		if err != nil {
			log.Println(err)
		}

		res, err := dm.DoHttpRequest(dm.Daemons[i], reqRawData)
		if err != nil {
			log.Println(err)
		}

		var result JsonRpcResponse
		err = json.NewDecoder(res.Body).Decode(&result)
		if err != nil {
			log.Println(err)
		}
		return res, &result, dm.Daemons[i]
	}

	return nil, nil, nil
}
