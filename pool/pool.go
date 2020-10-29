package pool

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/mining-pool/not-only-mining-pool/api"
	"github.com/mining-pool/not-only-mining-pool/bans"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/jobs"
	"github.com/mining-pool/not-only-mining-pool/p2p"
	"github.com/mining-pool/not-only-mining-pool/storage"
	"github.com/mining-pool/not-only-mining-pool/stratum"
	"github.com/mining-pool/not-only-mining-pool/utils"
)

var log = logging.Logger("pool")

type Pool struct {
	DaemonManager *daemons.DaemonManager
	JobManager    *jobs.JobManager
	P2PManager    *p2p.Peer

	StratumServer *stratum.Server

	Options                    *config.Options
	CoinPrecision              int
	HasGetInfo                 bool
	Stats                      *Stats
	BlockPollingIntervalTicker *time.Ticker
	Recipients                 []*config.Recipient
	ProtocolVersion            int
	APIServer                  *api.Server
}

func NewPool(options *config.Options) *Pool {
	dm := daemons.NewDaemonManager(options.Daemons, options.Coin)
	dm.Check()

	if options.PoolAddress.GetScript() == nil {
		log.Panicf("failed to get poolAddress' script, check the address and type")
	}

	for _, addr := range options.RewardRecipients {
		if addr.GetScript() == nil {
			log.Panicf("failed to get addr %s' script, check the address and type", addr.Address)
		}
	}

	var precision int = 8 //sat
	if !options.DisablePayment {
		_, getBalance, _, err := dm.Cmd("getbalance", []interface{}{})
		if err != nil {
			log.Panic(err)
		}

		if getBalance.Error != nil {
			log.Fatal(errors.New(fmt.Sprint(getBalance.Error)))
		}

		split := strings.Split(string(getBalance.Result), ".")
		precision = len(split[1])
		log.Warnf("coin precision is %d", precision)
	}

	db := storage.NewStorage(options.Coin.Name, options.Storage)

	jm := jobs.NewJobManager(options, dm, db)
	bm := bans.NewBanningManager(options.Banning)
	s := api.NewAPIServer(options, db)

	return &Pool{
		Options:       options,
		DaemonManager: dm,
		JobManager:    jm,
		APIServer:     s,

		StratumServer: stratum.NewStratumServer(options, jm, bm),
		CoinPrecision: precision,
		Stats:         NewStats(),
	}
}

//
func (p *Pool) Init() {
	p.CheckAllReady()
	p.DetectCoinData()

	initGBT, err := p.DaemonManager.GetBlockTemplate()
	if err != nil {
		log.Fatal(err)
	}

	p.SetupP2PBlockNotify()
	p.SetupBlockPolling()

	p.JobManager.Init(initGBT)

	p.StartStratumServer()
	p.APIServer.Serve()

	p.OutputPoolInfo()
}

func (p *Pool) SetupP2PBlockNotify() {
	if p.Options.P2P == nil {
		return
	}

	p.P2PManager = p2p.NewPeer(p.ProtocolVersion, p.Options.P2P)
	p.Init()

	go func() {
		for {
			blockHash, ok := <-p.P2PManager.BlockNotifyCh
			if !ok {
				log.Warn("Block notify is stopped!")
				return
			}

			if p.JobManager.CurrentJob != nil && blockHash != p.JobManager.CurrentJob.GetBlockTemplate.PreviousBlockHash {
				gbt, err := p.DaemonManager.GetBlockTemplate()
				if err != nil {
					log.Error("p2p block notify failed getting block template: ", err)
				}
				p.JobManager.ProcessTemplate(gbt)
			}
		}
	}()
}

func (p *Pool) AttachMiners(miners []*stratum.Client) {
	for i := range miners {
		p.StratumServer.ManuallyAddStratumClient(miners[i])
	}

	p.StratumServer.BroadcastCurrentMiningJob(p.JobManager.CurrentJob.GetJobParams(true))
}

func (p *Pool) StartStratumServer() {
	portStarted := p.StratumServer.Init()
	p.Stats.StratumPorts = portStarted
}

// enrich the config options from rpc
func (p *Pool) DetectCoinData() {
	var diff float64

	// getdifficulty
	_, rpcResponse, _, err := p.DaemonManager.Cmd("getdifficulty", []interface{}{})
	if err != nil {
		log.Panic(err)
	}
	if rpcResponse == nil || rpcResponse.Error != nil {
		log.Error("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
		return
	}
	getDifficulty := daemons.BytesToGetDifficulty(rpcResponse.Result)
	switch reflect.ValueOf(getDifficulty).Kind() {
	case reflect.Float64:
		diff = getDifficulty.(float64)
		p.Options.Coin.Reward = "POW"
	case reflect.Array:
		diff = getDifficulty.(map[string]interface{})["proof-of-work"].(float64)
		if p.Options.Coin.Reward == "" {
			if bytes.Contains(rpcResponse.Result, []byte("proof-of-stake")) {
				p.Options.Coin.Reward = "POS"
			} else {
				p.Options.Coin.Reward = "POW"
			}
		}
	default:
		log.Error(reflect.ValueOf(getDifficulty).Kind())
	}

	// getmininginfo
	_, rpcResponse, _, err = p.DaemonManager.Cmd("getmininginfo", []interface{}{})
	if err != nil {
		log.Panic(err)
	}

	if rpcResponse == nil || rpcResponse.Error != nil {
		log.Error("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
		return
	}
	getMiningInfo := daemons.BytesToGetMiningInfo(rpcResponse.Result)
	p.Stats.NetworkHashrate = getMiningInfo.Networkhashps

	_, rpcResponse, _, err = p.DaemonManager.Cmd("submitblock", []interface{}{})
	if err != nil {
		log.Panic(err)
	}

	if rpcResponse == nil || rpcResponse.Error == nil {
		log.Error("Could not start pool, error with init batch RPC call: " + utils.JsonifyIndentString(rpcResponse))
		return
	}

	if rpcResponse.Error.Message == "Method not found" {
		p.Options.Coin.NoSubmitBlock = true
	} else if rpcResponse.Error.Code == -1 {
		p.Options.Coin.NoSubmitBlock = false
	} else {
		log.Fatal("Could not detect block submission RPC method, " + utils.JsonifyIndentString(rpcResponse))
	}

	_, rpcResponse, _, err = p.DaemonManager.Cmd("getwalletinfo", []interface{}{})
	if err != nil {
		log.Panic(err)
	}

	if rpcResponse == nil || rpcResponse.Error != nil {
		log.Error("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
		return
	}

	_, rpcResponse, _, err = p.DaemonManager.Cmd("getinfo", []interface{}{})
	if err != nil {
		log.Panic(err)
	}

	if rpcResponse != nil && rpcResponse.Error == nil {
		getInfo := daemons.BytesToGetInfo(rpcResponse.Result)

		p.Options.Coin.Testnet = getInfo.Testnet
		p.ProtocolVersion = getInfo.Protocolversion
		// diff = getInfo.Difficulty

		p.Stats.Connections = getInfo.Connections
	} else {
		_, rpcResponse, _, err := p.DaemonManager.Cmd("getnetworkinfo", []interface{}{})
		if err != nil {
			log.Panic(err)
		}
		if rpcResponse.Error != nil || rpcResponse == nil {
			log.Error("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
			return
		}
		getNetworkInfo := daemons.BytesToGetNetworkInfo(rpcResponse.Result)

		_, rpcResponse, _, err = p.DaemonManager.Cmd("getblockchaininfo", []interface{}{})
		if err != nil {
			log.Panic(err)
		}
		if rpcResponse.Error != nil || rpcResponse == nil {
			log.Error("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
			return
		}
		getBlockchainInfo := daemons.BytesToGetBlockchainInfo(rpcResponse.Result)
		p.Options.Coin.Testnet = strings.Contains(getBlockchainInfo.Chain, "test")
		p.ProtocolVersion = getNetworkInfo.Protocolversion
		// diff = getBlockchainInfo.Difficulty

		p.Stats.Connections = getNetworkInfo.Connections
	}

	mul := 1 << p.Options.Algorithm.Multiplier
	p.Stats.Difficulty = diff * float64(mul)
}

func (p *Pool) OutputPoolInfo() {
	startMessage := "Stratum Pool Server Started for " + p.Options.Coin.Name + " [" + strings.ToUpper(p.Options.Coin.Symbol) + "] "

	var network string
	if p.Options.Coin.Testnet {
		network = "Testnet"
	} else {
		network = "Mainnet"
	}

	diff, _ := p.JobManager.CurrentJob.Difficulty.Float64()
	mul := 1 << p.Options.Algorithm.Multiplier

	infoLines := []string{
		startMessage,
		"Network Connected:\t" + network,
		"Detected Reward Type:\t" + p.Options.Coin.Reward,
		"Current Block Height:\t" + strconv.FormatInt(p.JobManager.CurrentJob.GetBlockTemplate.Height, 10),
		"Current Connect Peers:\t" + strconv.Itoa(p.Stats.Connections),
		"Current Block Diff:\t" + strconv.FormatFloat(diff*float64(mul), 'f', 7, 64),
		"Network Difficulty:\t" + strconv.FormatFloat(p.Stats.Difficulty, 'f', 7, 64),
		"Network Hash Rate:\t" + utils.GetReadableHashRateString(p.Stats.NetworkHashrate),
		"Stratum Port(s):\t" + string(utils.Jsonify(p.Stats.StratumPorts)),
		"Total Pool Fee Percent:\t" + strconv.FormatFloat(p.Options.TotalFeePercent(), 'f', 7, 64) + "%",
	}

	fmt.Println(strings.Join(infoLines, "\n\t"))
}

func (p *Pool) CheckAllReady() {
	results, _ := p.DaemonManager.CmdAll("getblocktemplate", []interface{}{map[string]interface{}{"capabilities": []string{"coinbasetxn", "workid", "coinbase/append"}, "rules": []string{"segwit"}}})
	for i := range results {
		if results[i] == nil {
			log.Fatalf("daemon %s is not available", p.DaemonManager.Daemons[i])
		}

		if results[i].Error != nil {
			log.Fatalf("daemon %s is not ready for mining: %s", p.DaemonManager.Daemons[i], results[i].Error.Message)
		}
	}
}

func (p *Pool) SetupBlockPolling() {
	if p.Options.BlockRefreshInterval <= 0 {
		log.Warn("Block template polling has been disabled")
		return
	}

	pollingInterval := time.Duration(p.Options.BlockRefreshInterval) * time.Second
	p.BlockPollingIntervalTicker = time.NewTicker(pollingInterval)

	go func() {
		for {
			_, ok := <-p.BlockPollingIntervalTicker.C
			if !ok {
				log.Warn("Block polling is stopped!")
				p.BlockPollingIntervalTicker.Stop()
				return
			}

			gbt, err := p.DaemonManager.GetBlockTemplate()
			if err != nil {
				log.Error("Block notify error getting block template for ", p.Options.Coin.Name, err)
			}

			if gbt != nil {
				p.JobManager.ProcessTemplate(gbt)
			}
		}
	}()
}
