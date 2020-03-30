package api

import (
	"encoding/json"
	"github.com/mining-pool/go-pool-server/config"
	"log"
	"net/http"
)

// TODO
type Server struct {
	options *config.Options
}

func NewAPIServer(options *config.Options) *Server {
	s := &Server{
		options: options,
	}

	http.HandleFunc("/", s.indexFunc)

	http.HandleFunc("/pool", s.poolFunc)

	http.HandleFunc("/config", s.configFunc)

	http.HandleFunc("/miner", s.minerFunc)

	return s
}

func (s *Server) Serve() {
	addr := s.options.API.Addr()
	log.Println("API server listening on", addr)
	go http.ListenAndServe(addr, nil)
}

func (s *Server) indexFunc(writer http.ResponseWriter, _ *http.Request) {

	raw, _ := json.Marshal([]string{"/pool", "/miner"})
	_, _ = writer.Write(raw)
}

func (s *Server) poolFunc(writer http.ResponseWriter, request *http.Request) {

}

func (s *Server) configFunc(writer http.ResponseWriter, _ *http.Request) {
	raw, _ := json.Marshal(s.options)
	_, _ = writer.Write(raw)
}

func (s *Server) minerFunc(writer http.ResponseWriter, request *http.Request) {

}
