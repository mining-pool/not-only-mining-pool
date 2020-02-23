package poolManager

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/mining-pool/go-pool-server/algorithm"
	"github.com/mining-pool/go-pool-server/banningManager"
	"github.com/mining-pool/go-pool-server/config"
	"github.com/mining-pool/go-pool-server/daemonManager"
	"github.com/mining-pool/go-pool-server/jobManager"
	"github.com/mining-pool/go-pool-server/p2pManager"
	"github.com/mining-pool/go-pool-server/stratum"
	"github.com/mining-pool/go-pool-server/utils"
	"log"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type Pool struct {
	DaemonManager *daemonManager.DaemonManager
	JobManager    *jobManager.JobManager
	P2PManager    *p2pManager.Peer

	StratumServer *stratum.Server

	Options                *config.Options
	Magnitude              uint64
	CoinPrecision          int
	HasGetInfo             bool
	Stats                  *Stats
	BlockPollingIntervalCh <-chan time.Time
	Recipients             []*config.Recipient
	ProtocolVersion        int
}

func NewPool(options *config.Options) *Pool {
	dm := daemonManager.NewDaemonManager(options.Daemons, options.Coin)
	dm.Check()

	_, validateAddress, daemon := dm.Cmd("validateaddress", []interface{}{options.PoolAddress.Address})
	if validateAddress.Error != nil {
		log.Fatal("Error with payment processing daemon: ", string(utils.Jsonify(daemon)), " error: ", utils.JsonifyIndentString(validateAddress.Error))
	}

	_, result, _ := dm.Cmd("getaddressinfo", []interface{}{options.PoolAddress.Address})

	if result.Error != nil {
		log.Fatal("Error with payment processing daemon, getaddressinfo failed ... ", utils.JsonifyIndentString(result.Error))
	}

	validateAddressResult := daemonManager.BytesToValidateAddress(result.Result)
	if !validateAddressResult.Ismine {
		log.Fatal("Daemon does not own poolManager address - payment processing can not be done with this daemon: ", utils.JsonifyIndentString(daemon))
	}

	_, getBalance, _ := dm.Cmd("getbalance", []interface{}{})

	if getBalance.Error != nil {
		log.Fatal(errors.New(fmt.Sprint(getBalance.Error)))
	}

	split0 := bytes.Split(utils.Jsonify(getBalance), []byte(`result":`))
	split2 := bytes.Split(split0[1], []byte(","))
	split3 := bytes.Split(split2[0], []byte("."))
	d := split3[1]

	magnitude, err := strconv.ParseInt("10"+strconv.Itoa(len(d))+"0", 10, 64)
	if err != nil {
		log.Fatal("Error detecting number of satoshis in a coin, cannot do payment processing. Tried parsing: ", string(utils.Jsonify(getBalance)))
	}

	jm := jobManager.NewJobManager(options, validateAddressResult, dm)
	bm := banningManager.NewBanningManager(options.Banning)

	return &Pool{
		Options:       options,
		DaemonManager: dm,
		JobManager:    jm,

		StratumServer: stratum.NewStratumServer(options, jm, bm),
		Magnitude:     uint64(magnitude),
		CoinPrecision: len(strconv.FormatUint(uint64(magnitude), 10)) - 1,
		Stats:         NewStats(),
	}
}

//
func (p *Pool) Init() {
	if !p.CheckAllSynced() {
		log.Fatal("Not synced!")
	}

	p.DetectCoinData()

	initGBT, err := p.DaemonManager.GetBlockTemplate()
	if err != nil {
		log.Fatal(err)
	}

	p.SetupP2PBlockNotify()
	p.SetupBlockPolling()

	p.JobManager.Init(initGBT)

	p.StartStratumServer()
	p.OutputPoolInfo()
}

func (p *Pool) SetupP2PBlockNotify() {
	if p.Options.P2P == nil || !p.Options.P2P.Enabled {
		return
	}

	p.P2PManager = p2pManager.NewPeer(p.ProtocolVersion, p.Options.P2P)
	p.Init()

	go func() {
		for {
			select {
			case blockHash, ok := <-p.P2PManager.BlockNotifyCh:
				if !ok {
					log.Println("Block notify is stopped!")
					return
				}

				if p.JobManager.CurrentJob != nil && blockHash != p.JobManager.CurrentJob.GetBlockTemplate.PreviousBlockHash {
					gbt, err := p.DaemonManager.GetBlockTemplate()
					if err != nil {
						log.Println("Block notify error getting block template: ", err)
					}
					p.JobManager.ProcessTemplate(gbt)
				}
			}
		}
	}()
}

func (p *Pool) AttachMiners(miners []*stratum.Client) {
	for i := range miners {
		p.StratumServer.ManuallyAddStratumClient(miners[i])
	}

	p.StratumServer.BroadcastMiningJobs(p.JobManager.CurrentJob.GetJobParams())
}

func (p *Pool) StartStratumServer() {
	portStarted := p.StratumServer.Init()
	p.Stats.StratumPorts = portStarted
}

// enrich the config options from rpc
func (p *Pool) DetectCoinData() {
	var diff float64

	// getdifficulty
	_, rpcResponse, _ := p.DaemonManager.Cmd("getdifficulty", []interface{}{})
	if rpcResponse.Error != nil || rpcResponse == nil {
		log.Println("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
		return
	}
	getDifficulty := daemonManager.BytesToGetDifficulty(rpcResponse.Result)
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
		log.Println(reflect.ValueOf(getDifficulty).Kind())
	}

	// validateaddress
	_, rpcResponse, _ = p.DaemonManager.Cmd("validateaddress", []interface{}{p.Options.PoolAddress.Address})
	if rpcResponse.Error != nil || rpcResponse == nil {
		log.Println("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
		return
	}
	validateAddress := daemonManager.BytesToValidateAddress(rpcResponse.Result)
	if !validateAddress.Isvalid {
		log.Fatal("Daemon reports address is not valid")
	}
	p.Options.PoolAddressScript = jobManager.GetPoolAddressScript(p.Options.Coin.Reward, validateAddress)
	if p.Options.Coin.Reward == "POS" && validateAddress.Pubkey == "" {
		log.Fatal("The address provided is not from the daemon wallet - this is required for POS coins.")
	}

	// getmininginfo
	_, rpcResponse, _ = p.DaemonManager.Cmd("getmininginfo", []interface{}{})
	if rpcResponse.Error != nil || rpcResponse == nil {
		log.Println("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
		return
	}
	getMiningInfo := daemonManager.BytesToGetMiningInfo(rpcResponse.Result)
	p.Stats.NetworkHashrate = getMiningInfo.Networkhashps

	_, rpcResponse, _ = p.DaemonManager.Cmd("submitblock", []interface{}{})
	if rpcResponse == nil || rpcResponse.Error == nil {
		log.Println("Could not start pool, error with init batch RPC call: " + utils.JsonifyIndentString(rpcResponse))
		return
	}

	if rpcResponse.Error.Message == "Method not found" {
		p.Options.NoSubmitMethod = true
	} else if rpcResponse.Error.Code == -1 {
		p.Options.NoSubmitMethod = false
	} else {
		log.Fatal("Could not detect block submission RPC method, " + utils.JsonifyIndentString(rpcResponse))
	}

	_, rpcResponse, _ = p.DaemonManager.Cmd("getwalletinfo", []interface{}{})
	if rpcResponse.Error != nil || rpcResponse == nil {
		log.Println("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
		return
	}

	if p.Options.Coin.NoGetBlockchainInfo {
		_, rpcResponse, _ := p.DaemonManager.Cmd("getinfo", []interface{}{})
		if rpcResponse.Error != nil || rpcResponse == nil {
			log.Println("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
			return
		}
		getInfo := daemonManager.BytesToGetInfo(rpcResponse.Result)

		p.Options.Testnet = getInfo.Testnet
		p.ProtocolVersion = getInfo.Protocolversion
		//diff = getInfo.Difficulty

		p.Stats.Connections = getInfo.Connections
	} else {
		_, rpcResponse, _ := p.DaemonManager.Cmd("getnetworkinfo", []interface{}{})
		if rpcResponse.Error != nil || rpcResponse == nil {
			log.Println("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
			return
		}
		getNetworkInfo := daemonManager.BytesToGetNetworkInfo(rpcResponse.Result)

		_, rpcResponse, _ = p.DaemonManager.Cmd("getblockchaininfo", []interface{}{})
		if rpcResponse.Error != nil || rpcResponse == nil {
			log.Println("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
			return
		}
		getBlockchainInfo := daemonManager.BytesToGetBlockchainInfo(rpcResponse.Result)
		p.Options.Testnet = strings.Contains(getBlockchainInfo.Chain, "test")
		p.ProtocolVersion = getNetworkInfo.Protocolversion
		//diff = getBlockchainInfo.Difficulty

		p.Stats.Connections = getNetworkInfo.Connections
	}

	p.Stats.Difficulty = diff * algorithm.Multiplier
}

func (p *Pool) OutputPoolInfo() {
	startMessage := "Stratum Pool Server Started for " + p.Options.Coin.Name + " [" + strings.ToUpper(p.Options.Coin.Symbol) + "] "

	var network string
	if p.Options.Testnet {
		network = "Testnet"
	} else {
		network = "Mainnet"
	}

	diff, _ := p.JobManager.CurrentJob.Difficulty.Float64()
	infoLines := []string{
		startMessage,
		"Network Connected:\t" + network,
		"Detected Reward Type:\t" + p.Options.Coin.Reward,
		"Current Block Height:\t" + strconv.FormatInt(p.JobManager.CurrentJob.GetBlockTemplate.Height, 10),
		"Current Connect Peers:\t" + strconv.Itoa(p.Stats.Connections),
		"Current Block Diff:\t" + strconv.FormatFloat(diff*algorithm.Multiplier, 'f', 7, 64),
		"Network Difficulty:\t" + strconv.FormatFloat(p.Stats.Difficulty, 'f', 7, 64),
		"Network Hash Rate:\t" + utils.GetReadableHashRateString(p.Stats.NetworkHashrate),
		"Stratum Port(s):\t" + string(utils.Jsonify(p.Stats.StratumPorts)),
		"Pool Fee Percent:\t" + strconv.FormatFloat(p.Options.FeePercent, 'f', 7, 64) + "%",
	}

	fmt.Println(strings.Join(infoLines, "\n\t"))
}

func (p *Pool) CheckAllSynced() bool {
	hasOneNotSynced := false
	_, results := p.DaemonManager.CmdAll("getblocktemplate", []interface{}{map[string]interface{}{"capabilities": []string{"coinbasetxn", "workid", "coinbase/append"}, "rules": []string{"segwit"}}})
	for i := range results {
		if results[i].Error != nil {
			hasOneNotSynced = true
		}
	}

	isAllSynced := !hasOneNotSynced
	return isAllSynced
}

func (p *Pool) SetupBlockPolling() {
	if p.Options.BlockRefreshInterval <= 0 {
		log.Println("Block template polling has been disabled")
		return
	}

	pollingInterval := time.Duration(p.Options.BlockRefreshInterval) * time.Second
	p.BlockPollingIntervalCh = time.Tick(pollingInterval)

	go func() {
		for {
			select {
			case _, ok := <-p.BlockPollingIntervalCh:
				if !ok {
					log.Println("Block polling is stopped!")
					return
				}

				gbt, err := p.DaemonManager.GetBlockTemplate()
				if err != nil {
					log.Println("Block notify error getting block template for "+p.Options.Coin.Name, err)
				}

				if gbt != nil {
					p.JobManager.ProcessTemplate(gbt)
				}
			}
		}
	}()
}
