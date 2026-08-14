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

	"github.com/mining-pool/not-only-mining-pool/algorithm"
	"github.com/mining-pool/not-only-mining-pool/api"
	"github.com/mining-pool/not-only-mining-pool/bans"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/jobs"
	"github.com/mining-pool/not-only-mining-pool/p2p"
	"github.com/mining-pool/not-only-mining-pool/payments"
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
	Magnitude                  uint64
	CoinPrecision              int
	HasGetInfo                 bool
	Stats                      *Stats
	BlockPollingIntervalTicker *time.Ticker
	Recipients                 []*config.Recipient
	ProtocolVersion            int
	APIServer                  *api.Server
	PaymentManager             *payments.PaymentManager

	// Engine is set for non-GBT mining models (e.g. ethash). When set, Init
	// skips the entire Bitcoin daemon/jobmanager/payments machinery.
	Engine engine.Engine
}

// NewEnginePool builds a pool driven by a pluggable engine.Engine (e.g. ethash)
// instead of the Bitcoin getblocktemplate flow. It wires only the shared
// infrastructure — stratum server, storage, banning, API — plus the engine;
// there is no bitcoin DaemonManager/JobManager/payment path.
func NewEnginePool(options *config.Options) *Pool {
	eng, ok := engine.Get(strings.ToLower(options.Engine))
	if !ok {
		log.Panicf("engine %q is not registered; build with the matching build tag (e.g. -tags ethash) to include it. Registered engines: %v", options.Engine, engine.Registered())
	}

	if err := eng.Init(options); err != nil {
		log.Fatal("engine init failed: ", err)
	}

	db := storage.NewStorage(options.Coin.Name, options.Storage)
	bm := bans.NewBanningManager(options.Banning)
	apiServer := api.NewAPIServer(options, db)

	ss := stratum.NewStratumServer(options, nil, bm)
	ss.Engine = eng
	ss.DB = db // engine-mode share persistence (stats/accounting)

	return &Pool{
		Options:       options,
		Engine:        eng,
		APIServer:     apiServer,
		StratumServer: ss,
		Stats:         NewStats(),
	}
}

func NewPool(options *config.Options) *Pool {
	// NewPool is the Bitcoin/GBT path; other engines must go through NewEnginePool.
	if eng := strings.ToLower(options.Engine); eng != "" && eng != "gbt" {
		log.Panicf("engine %q must be constructed via pool.NewEnginePool (main dispatches on the \"engine\" config field)", options.Engine)
	}

	if !algorithm.IsSupported(options.Algorithm.Name) {
		log.Panicf("algorithm %q is not supported, supported: %s", options.Algorithm.Name, strings.Join(algorithm.SupportedAlgorithms(), ", "))
	}
	if bh := options.Algorithm.BlockHasher; bh != "" && !algorithm.IsSupported(bh) {
		log.Panicf("blockHasher %q is not supported, supported: %s", bh, strings.Join(algorithm.SupportedAlgorithms(), ", "))
	}
	// allow configs to omit "multiplier": fall back to the algorithm's conventional value.
	if options.Algorithm.Multiplier == 0 {
		options.Algorithm.Multiplier = int(algorithm.DefaultMultiplier(options.Algorithm.Name))
	}
	// pay expensive one-off init (e.g. verthash.dat) at startup, not on first share
	algorithm.Warmup(options.Algorithm.Name)

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

	var magnitude int64 = 100000000 //sat
	if !options.DisablePayment {
		_, getBalance, _ := dm.Cmd("getbalance", []interface{}{})

		if getBalance.Error != nil {
			log.Fatal(errors.New(fmt.Sprint(getBalance.Error)))
		}

		split0 := bytes.Split(utils.Jsonify(getBalance), []byte(`result":`))
		split2 := bytes.Split(split0[1], []byte(","))
		split3 := bytes.Split(split2[0], []byte("."))
		d := split3[1]

		var err error
		magnitude, err = strconv.ParseInt("10"+strconv.Itoa(len(d))+"0", 10, 64)
		if err != nil {
			log.Fatal("ErrorCode detecting number of satoshis in a coin, cannot do payments processing. Tried parsing: ", string(utils.Jsonify(getBalance)))
		}
	}

	db := storage.NewStorage(options.Coin.Name, options.Storage)

	jm := jobs.NewJobManager(options, dm, db)
	bm := bans.NewBanningManager(options.Banning)
	s := api.NewAPIServer(options, db)
	pm := payments.NewPaymentManager(options.PaymentOptions, options.PoolAddress, dm, db)

	return &Pool{
		Options:        options,
		DaemonManager:  dm,
		JobManager:     jm,
		APIServer:      s,
		PaymentManager: pm,

		StratumServer: stratum.NewStratumServer(options, jm, bm),
		Magnitude:     uint64(magnitude),
		CoinPrecision: len(strconv.FormatUint(uint64(magnitude), 10)) - 1,
		Stats:         NewStats(),
	}
}

func (p *Pool) Init() {
	if p.Engine != nil {
		// Engine-driven pool: the engine already fetched its first work in
		// NewEnginePool; just serve stratum + API. No GBT/p2p/payment machinery.
		p.StartStratumServer()
		p.APIServer.Serve()
		log.Warnf("Stratum Pool Server Started for %s [%s] using the %q engine, serving ports %v",
			p.Options.Coin.Name, strings.ToUpper(p.Options.Coin.Symbol), p.Engine.Name(), p.Stats.StratumPorts)
		return
	}

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

	if !p.Options.DisablePayment && p.Options.PaymentOptions != nil {
		if err := p.PaymentManager.Init(); err != nil {
			log.Fatal("payment processing init failed: ", err)
		}
		go p.PaymentManager.Serve()
	}

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
	_, rpcResponse, _ := p.DaemonManager.Cmd("getdifficulty", []interface{}{})
	if rpcResponse.Error != nil || rpcResponse == nil {
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
	_, rpcResponse, _ = p.DaemonManager.Cmd("getmininginfo", []interface{}{})
	if rpcResponse.Error != nil || rpcResponse == nil {
		log.Error("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
		return
	}
	getMiningInfo := daemons.BytesToGetMiningInfo(rpcResponse.Result)
	p.Stats.NetworkHashrate = getMiningInfo.Networkhashps

	_, rpcResponse, _ = p.DaemonManager.Cmd("submitblock", []interface{}{})
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

	_, rpcResponse, _ = p.DaemonManager.Cmd("getwalletinfo", []interface{}{})
	if rpcResponse.Error != nil || rpcResponse == nil {
		log.Error("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
		return
	}

	_, rpcResponse, _ = p.DaemonManager.Cmd("getinfo", []interface{}{})
	if rpcResponse.Error == nil && rpcResponse != nil {
		getInfo := daemons.BytesToGetInfo(rpcResponse.Result)

		p.Options.Coin.Testnet = getInfo.Testnet
		p.ProtocolVersion = getInfo.Protocolversion
		// diff = getInfo.Difficulty

		p.Stats.Connections = getInfo.Connections
	} else {
		_, rpcResponse, _ := p.DaemonManager.Cmd("getnetworkinfo", []interface{}{})
		if rpcResponse.Error != nil || rpcResponse == nil {
			log.Error("Could not start pool, error with init batch RPC call: " + string(utils.Jsonify(rpcResponse)))
			return
		}
		getNetworkInfo := daemons.BytesToGetNetworkInfo(rpcResponse.Result)

		_, rpcResponse, _ = p.DaemonManager.Cmd("getblockchaininfo", []interface{}{})
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
	rules := []string{"segwit"}
	if p.Options.Coin.GBTRules != nil {
		rules = p.Options.Coin.GBTRules
	}
	_, results := p.DaemonManager.CmdAll("getblocktemplate", []interface{}{map[string]interface{}{"capabilities": []string{"coinbasetxn", "workid", "coinbase/append"}, "rules": rules}})
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
