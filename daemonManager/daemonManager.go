package daemonManager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"

	logging "github.com/ipfs/go-log/v2"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/utils"
)

var log = logging.Logger("daemonMgr")

type DaemonManager struct {
	Daemons []*config.DaemonOptions
	clients map[string]*http.Client
	Coin    *config.CoinOptions
}

func NewDaemonManager(daemons []*config.DaemonOptions, coin *config.CoinOptions) *DaemonManager {
	if daemons == nil || coin == nil {
		log.Fatal("new daemon with empty options!")
	}

	clients := make(map[string]*http.Client)
	for _, daemon := range daemons {
		transport := &http.Transport{}
		if daemon.TLS != nil {
			transport.TLSClientConfig = daemon.TLS.ToTLSConfig()
		}

		client := &http.Client{Transport: transport}
		clients[daemon.String()] = client
	}

	return &DaemonManager{
		Daemons: daemons,
		Coin:    coin,
		clients: clients,
	}
}

func (dm *DaemonManager) Check() {
	if !dm.IsAllOnline() {
		log.Fatal("daemons are not all online!")
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
			log.Error(err)
			return false
		}

		if jsonRes.Error != nil {
			log.Error(jsonRes.Error)
			return false
		}

	}

	return true
}

func (dm *DaemonManager) DoHttpRequest(daemon *config.DaemonOptions, reqRawData []byte) (*http.Response, error) {
	client := dm.clients[daemon.String()]

	req, err := http.NewRequest("POST", daemon.URL(), bytes.NewReader(reqRawData))
	if err != nil {
		log.Panic(err)
	}
	if daemon.User != "" {
		req.SetBasicAuth(daemon.User, daemon.Password)
	}

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

		fmt.Println(string(reqRawData))
		res, err := dm.DoHttpRequest(daemon, reqRawData)
		if err != nil {
			log.Fatal(err)
		}
		//if err := dm.CheckStatusCode(res.StatusCode); err != nil {
		//	log.Println(err)
		//}

		responses = append(responses, res)

		var result JsonRpcResponse
		raw, err := ioutil.ReadAll(res.Body)
		if err != nil {
			log.Error(err)
		}
		res.Body = ioutil.NopCloser(bytes.NewBuffer(raw))

		err = json.Unmarshal(raw, &result)
		if err != nil {
			log.Panicf("failed to unmarshal response body: %s", raw)
		}
		results = append(results, &result)
	}

	return responses, results
}

func (dm *DaemonManager) CheckStatusCode(statusCode int) error {
	switch statusCode / 100 {
	case 2:
		return nil
	case 4:
		switch statusCode % 100 {
		case 0:
			return errors.New("daemon cannot understand the request")
		case 1:
			return errors.New("daemon requires authorization (have to login before request)")
		case 3:
			return errors.New("daemon rejected the request")

		case 4:
			return errors.New("daemon cannot find the resource requested")
		case 13:
			return errors.New("daemon cannot deal this large request")

		}

	case 5:
		return errors.New("daemon internal error")
	}

	return errors.New("unknown status code:" + strconv.Itoa(statusCode))
}

func (dm *DaemonManager) Cmd(method string, params []interface{}) (*config.DaemonOptions, *JsonRpcResponse, *http.Response) {
	for i := 0; i < len(dm.Daemons); i++ {
		reqRawData, err := json.Marshal(map[string]interface{}{
			"id":     utils.RandPositiveInt64(),
			"method": method,
			"params": params,
		})
		if err != nil {
			log.Error(err)
		}

		res, err := dm.DoHttpRequest(dm.Daemons[i], reqRawData)
		if err != nil {
			log.Error(err)
		}
		//if err := dm.CheckStatusCode(res.StatusCode); err != nil {
		//	log.Println(err)
		//}

		var result JsonRpcResponse
		raw, err := ioutil.ReadAll(res.Body)
		if err != nil {
			log.Error(err)
		}
		res.Body = ioutil.NopCloser(bytes.NewBuffer(raw))

		err = json.Unmarshal(raw, &result)
		if err != nil {
			log.Error(err)
		}
		return dm.Daemons[i], &result, res
	}

	log.Error("failed getting GBT from all daemons!")
	return nil, nil, nil
}
