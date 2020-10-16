package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	logging "github.com/ipfs/go-log/v2"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/storage"
)

var log = logging.Logger("api")

// TODO
type Server struct {
	*mux.Router

	apiConf *config.APIOptions
	storage *storage.DB

	availablePaths []string
	config         map[string]interface{}
}

// NewAPIServer creates a Server which follows [standard api](https://github.com/mining-pool/mining-pool-api)
func NewAPIServer(options *config.Options, storage *storage.DB) *Server {
	s := &Server{
		Router: mux.NewRouter(),

		apiConf: options.API,
		storage: storage,

		availablePaths: make([]string, 0),
		config:         make(map[string]interface{}),
	}

	s.ConvertConf(options)

	s.RegisterFunc("/", s.indexFunc)

	s.RegisterFunc("/pool", s.poolFunc)

	s.RegisterFunc("/config", s.configIndexFunc)
	s.RegisterFunc("/config/{key}", s.configFunc)

	s.RegisterFunc("/miner/{miner}", s.minerFunc)
	s.RegisterFunc("/miner/{miner}/rig/{rig}", s.rigFunc)
	s.Use(mux.CORSMethodMiddleware(s.Router))

	http.Handle("/", s)

	return s
}

func (s *Server) ConvertConf(options *config.Options) {
	s.config["ports"] = options.Ports
	s.config["algorithm"] = options.Algorithm
	s.config["coin"] = options.Coin
	s.config["api"] = options.API
}

func (s *Server) RegisterFunc(path string, fn func(http.ResponseWriter, *http.Request)) {
	s.HandleFunc(path, fn)
	s.availablePaths = append(s.availablePaths, path)
}

func (s *Server) Serve() {
	addr := s.apiConf.Addr()
	log.Warn("API server listening on ", addr)
	go func() {
		err := http.ListenAndServe(addr, nil)
		if err != nil {
			panic(err)
		}
	}()
}

func (s *Server) indexFunc(writer http.ResponseWriter, _ *http.Request) {
	raw, _ := json.Marshal(s.availablePaths)
	_, _ = writer.Write(raw)
}

func (s *Server) configIndexFunc(writer http.ResponseWriter, _ *http.Request) {
	keys := make([]string, 0)
	for k := range s.config {
		keys = append(keys, "/config/"+k)
	}

	raw, _ := json.Marshal(keys)
	_, _ = writer.Write(raw)
}

func (s *Server) configFunc(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	raw, _ := json.Marshal(s.config[vars["key"]])
	_, _ = writer.Write(raw)
}

type PoolInfo struct {
	CoinName string `json:"coinName"`

	Hashrate1Min  float64 `json:"hashrate1min"`
	Hashrate30Min float64 `json:"hashrate30min"`
	Hashrate1H    float64 `json:"hashrate1h"`
	Hashrate6H    float64 `json:"hashrate6h"`
	Hashrate1D    float64 `json:"hashrate1d"`

	Miners []string `json:"miners"`
}

func (s *Server) poolFunc(w http.ResponseWriter, _ *http.Request) {
	now := time.Now().Unix() // unit: sec

	hs1M, err := s.storage.GetPoolHashrate(now-60, now)
	if err != nil {
		log.Error(err)
	}
	hs30M, err := s.storage.GetPoolHashrate(now-30*60, now)
	if err != nil {
		log.Error(err)
	}
	hs1H, err := s.storage.GetPoolHashrate(now-60*60, now)
	if err != nil {
		log.Error(err)
	}
	hs6H, err := s.storage.GetPoolHashrate(now-6*60*60, now)
	if err != nil {
		log.Error(err)
	}
	hs1D, err := s.storage.GetPoolHashrate(now-24*60*60, now)
	if err != nil {
		log.Error(err)
	}

	miners, err := s.storage.GetMinerIndex()
	if err != nil {
		log.Error(err)
	}

	miner := PoolInfo{
		Hashrate1Min:  hs1M,
		Hashrate30Min: hs30M,
		Hashrate1H:    hs1H,
		Hashrate6H:    hs6H,
		Hashrate1D:    hs1D,

		Miners: miners,
	}

	raw, err := json.Marshal(&miner)
	if err != nil {
		log.Error(err)
	}

	_, _ = w.Write(raw)
}

type MinerInfo struct {
	Name string `json:"name"`

	Hashrate1Min  float64 `json:"hashrate1min"`
	Hashrate30Min float64 `json:"hashrate30min"`
	Hashrate1H    float64 `json:"hashrate1h"`
	Hashrate6H    float64 `json:"hashrate6h"`
	Hashrate1D    float64 `json:"hashrate1d"`

	RoundContrib float64  `json:"roundContrib"`
	Rigs         []string `json:"rigs"`
}

func (s *Server) minerFunc(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	minerName := vars["miner"]

	contrib, err := s.storage.GetMinerCurrentRoundContrib(minerName)
	if err != nil {
		log.Error(err)
	}

	now := time.Now().Unix() // unit: sec

	hs1M, err := s.storage.GetMinerHashrate(minerName, now-60, now)
	if err != nil {
		log.Error(err)
	}
	hs30M, err := s.storage.GetMinerHashrate(minerName, now-30*60, now)
	if err != nil {
		log.Error(err)
	}
	hs1H, err := s.storage.GetMinerHashrate(minerName, now-60*60, now)
	if err != nil {
		log.Error(err)
	}
	hs6H, err := s.storage.GetMinerHashrate(minerName, now-6*60*60, now)
	if err != nil {
		log.Error(err)
	}
	hs1D, err := s.storage.GetMinerHashrate(minerName, now-24*60*60, now)
	if err != nil {
		log.Error(err)
	}

	rigs, err := s.storage.GetRigIndex(minerName)
	if err != nil {
		log.Error(err)
	}

	miner := MinerInfo{
		Name: minerName,

		Hashrate1Min:  hs1M,
		Hashrate30Min: hs30M,
		Hashrate1H:    hs1H,
		Hashrate6H:    hs6H,
		Hashrate1D:    hs1D,

		RoundContrib: contrib,
		Rigs:         rigs,
	}

	raw, err := json.Marshal(&miner)
	if err != nil {
		log.Error(err)
	}

	_, _ = w.Write(raw)
}

type RigInfo struct {
	Name          string  `json:"name"`
	MinerName     string  `json:"minerName"`
	Hashrate1Min  float64 `json:"hashrate1min"`
	Hashrate30Min float64 `json:"hashrate30min"`
	Hashrate1H    float64 `json:"hashrate1h"`
	Hashrate6H    float64 `json:"hashrate6h"`
	Hashrate1D    float64 `json:"hashrate1d"`
}

func (s *Server) rigFunc(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	minerName := vars["miner"]
	rigName := vars["rig"]

	now := time.Now().Unix() // unit: sec

	hs1M, err := s.storage.GetRigHashrate(minerName, rigName, now-60, now)
	if err != nil {
		log.Error(err)
	}
	hs30M, err := s.storage.GetRigHashrate(minerName, rigName, now-30*60, now)
	if err != nil {
		log.Error(err)
	}
	hs1H, err := s.storage.GetRigHashrate(minerName, rigName, now-60*60, now)
	if err != nil {
		log.Error(err)
	}
	hs6H, err := s.storage.GetRigHashrate(minerName, rigName, now-6*60*60, now)
	if err != nil {
		log.Error(err)
	}
	hs1D, err := s.storage.GetRigHashrate(minerName, rigName, now-24*60*60, now)
	if err != nil {
		log.Error(err)
	}

	miner := RigInfo{
		Name:      rigName,
		MinerName: minerName,

		Hashrate1Min:  hs1M,
		Hashrate30Min: hs30M,
		Hashrate1H:    hs1H,
		Hashrate6H:    hs6H,
		Hashrate1D:    hs1D,
	}

	raw, err := json.Marshal(&miner)
	if err != nil {
		log.Error(err)
	}

	_, _ = w.Write(raw)
}
