package stratum

import (
	"crypto/tls"
	"encoding/binary"
	"github.com/node-standalone-pool/go-pool-server/banningManager"
	"github.com/node-standalone-pool/go-pool-server/config"
	"github.com/node-standalone-pool/go-pool-server/daemonManager"
	"github.com/node-standalone-pool/go-pool-server/jobManager"
	"github.com/node-standalone-pool/go-pool-server/vardiff"
	"log"
	"net"
	"strconv"
	"time"
)

type Server struct {
	Options    *config.Options
	Listener   net.Listener
	TLSOptions *tls.Config

	DaemonManager       *daemonManager.DaemonManager
	VarDiff             *vardiff.VarDiff
	JobManager          *jobManager.JobManager
	StratumClients      map[uint64]*Client
	SubscriptionCounter *SubscriptionCounter
	BanningManager      *banningManager.BanningManager

	RebroadcastTimeoutCh <-chan time.Time
}

func NewStratumServer(options *config.Options, jm *jobManager.JobManager, bm *banningManager.BanningManager) *Server {
	return &Server{
		Options:             options,
		BanningManager:      bm,
		TLSOptions:          nil,
		SubscriptionCounter: NewSubscriptionCounter(),

		JobManager:     jm,
		StratumClients: make(map[uint64]*Client),
	}
}

func (ss *Server) Init() (portStarted []int) {
	if ss.Options.Banning != nil && ss.Options.Banning.Enabled {
		ss.BanningManager.Init()
	}

	for port, options := range ss.Options.Ports {
		var err error
		if options.TLS {
			ss.Listener, err = tls.Listen("tcp", ":"+strconv.Itoa(port), ss.TLSOptions)
		} else {
			ss.Listener, _ = net.Listen("tcp", ":"+strconv.Itoa(port))
		}

		if err != nil {
			log.Println(err)
			continue
		}

		portStarted = append(portStarted, port)
		//if len(portStarted) == len(ss.Options.Ports) {
		//	// emit started
		//}
	}

	if len(portStarted) == 0 {
		log.Fatal("No port listened")
	}

	go func() {
		ss.RebroadcastTimeoutCh = time.Tick(time.Duration(ss.Options.JobRebroadcastTimeout) * time.Second)
		for {
			select {
			case <-ss.RebroadcastTimeoutCh:
				ss.BroadcastMiningJobs(ss.JobManager.CurrentJob.GetJobParams())
			}
		}
	}()

	go func() {
		for {
			conn, err := ss.Listener.Accept()
			if err != nil {
				log.Println(err)
				continue
			}

			if conn != nil {
				go ss.HandleNewClient(conn)
			}
		}
	}()

	return portStarted
}

func (ss *Server) HandleNewClient(socket net.Conn) []byte {
	subscriptionId := ss.SubscriptionCounter.Next()
	client := NewStratumClient(subscriptionId, socket, ss.Options, ss.JobManager, ss.BanningManager)
	ss.StratumClients[binary.LittleEndian.Uint64(subscriptionId)] = client
	// client.connected

	go func() {
		for {
			select {
			case <-client.SocketClosedEvent:
				ss.RemoveStratumClientBySubscriptionId(subscriptionId)
				// client.disconnected
			}
		}
	}()

	client.Init()

	return subscriptionId
}

func (ss *Server) BroadcastMiningJobs(jobParams []interface{}) {
	log.Println("Start broadcasting due to rebroadcast timeout")
	for clientId := range ss.StratumClients {
		ss.StratumClients[clientId].SendMiningJob(jobParams)
	}

	ss.RebroadcastTimeoutCh = time.Tick(time.Duration(ss.Options.JobRebroadcastTimeout) * time.Second) // clearTimeout
}

func (ss *Server) RemoveStratumClientBySubscriptionId(subscriptionId []byte) {
	delete(ss.StratumClients, binary.LittleEndian.Uint64(subscriptionId))
}

func (ss *Server) ManuallyAddStratumClient(client *Client) {
	subscriptionId := ss.HandleNewClient(client.Socket)
	if subscriptionId != nil {
		ss.StratumClients[binary.LittleEndian.Uint64(subscriptionId)].ManuallyAuthClient(client.WorkerName, client.WorkerPass)
		ss.StratumClients[binary.LittleEndian.Uint64(subscriptionId)].ManuallySetValues(client)
	}
}
