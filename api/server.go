package api

import (
	"encoding/json"
	"github.com/gorilla/mux"
	logging "github.com/ipfs/go-log"
	"github.com/mining-pool/go-pool-server/config"
	"github.com/mining-pool/go-pool-server/storage"
	"net/http"
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

	s.RegisterFunc("/miner", s.minerIndexFunc)
	//s.RegisterFunc("/miner/{miner}", s.minerFunc)
	//s.RegisterFunc("/miner/{miner}/{rig}", s.minerRigFunc)
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
	go http.ListenAndServe(addr, nil)
}

func (s *Server) indexFunc(writer http.ResponseWriter, _ *http.Request) {
	raw, _ := json.Marshal(s.availablePaths)
	_, _ = writer.Write(raw)
}

func (s *Server) poolFunc(writer http.ResponseWriter, request *http.Request) {

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

func (s *Server) minerIndexFunc(writer http.ResponseWriter, r *http.Request) {

}
