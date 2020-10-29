package daemons

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"sync"

	logging "github.com/ipfs/go-log/v2"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/utils"
)

var log = logging.Logger("daemons")

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
	_, responses := dm.CmdAll("getpeerinfo", []interface{}{})
	for _, res := range responses {
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

// BatchCmd will run a batch cmd on the wallet's rpc server
// the batch cmd is called on daemons one by one and return the first successful response
// batch cmd is list/set of normal rpc cmd, but batched into one rpc request
// the response is also a list
func (dm *DaemonManager) BatchCmd(commands []interface{}) (int, []*JsonRpcResponse, error) {
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
			return i, nil, err
		}
		var rpcResponses []*JsonRpcResponse
		err = json.NewDecoder(res.Body).Decode(&rpcResponses)
		if err != nil {
			return i, nil, err
		}

		return i, rpcResponses, err
	}

	return -1, nil, nil
}

// CmdAll sends the rpc call to all daemon, and never break because of any error.
// So the elem in responses may be nil
func (dm *DaemonManager) CmdAll(method string, params []interface{}) (results []*JsonRpcResponse, responses []*http.Response) {
	responses = make([]*http.Response, len(dm.Daemons))
	results = make([]*JsonRpcResponse, len(dm.Daemons))

	msg := map[string]interface{}{
		"id":     utils.RandPositiveInt64(),
		"method": method,
		"params": params,
	}

	reqRawData, err := json.Marshal(msg)
	if err != nil {
		log.Errorf("failed marshaling %v: %s", msg, err)
		return // all elem are nil
	}

	log.Debug(string(reqRawData))

	wg := sync.WaitGroup{}
	for i := range dm.Daemons {
		wg.Add(1)
		go func(i int) {
			res, err := dm.DoHttpRequest(dm.Daemons[i], reqRawData)
			if err != nil {
				log.Errorf("failed on daemon %s: %s", dm.Daemons[i].String(), err)
				return
			}

			//if err := dm.CheckStatusCode(res.StatusCode); err != nil {
			//	log.Println(err)
			//}

			responses[i] = res

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

			results[i] = &result

			wg.Done()
		}(i)
	}

	wg.Wait()

	return results, responses
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

// Cmd will call daemons one by one and return the first answer
// one by one not all is to try fetching from the same one not random one
func (dm *DaemonManager) Cmd(method string, params []interface{}) (int, *JsonRpcResponse, *http.Response, error) {
	reqRawData, err := json.Marshal(map[string]interface{}{
		"id":     utils.RandPositiveInt64(),
		"method": method,
		"params": params,
	})
	if err != nil {
		log.Error(err)
	}

	var result JsonRpcResponse
	var res *http.Response
	for i := range dm.Daemons {
		var err error
		res, err = dm.DoHttpRequest(dm.Daemons[i], reqRawData)
		if err != nil {
			log.Error(err)
		}

		//if err := dm.CheckStatusCode(res.StatusCode); err != nil {
		//	log.Println(err)
		//}

		raw, err := ioutil.ReadAll(res.Body)
		if err != nil {
			log.Error(err)
		}
		res.Body = ioutil.NopCloser(bytes.NewBuffer(raw))

		err = json.Unmarshal(raw, &result)
		if err != nil {
			log.Error(err)
		}

		return i, &result, res, nil
	}

	return -1, nil, nil, fmt.Errorf("all daemons are down") // avoid nil panic
}
