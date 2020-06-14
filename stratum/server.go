package stratum

import (
	"crypto/tls"
	"encoding/binary"
	logging "github.com/ipfs/go-log"
	"net"
	"strconv"
	"time"

	"github.com/mining-pool/go-pool-server/banningManager"
	"github.com/mining-pool/go-pool-server/config"
	"github.com/mining-pool/go-pool-server/daemonManager"
	"github.com/mining-pool/go-pool-server/jobManager"
	"github.com/mining-pool/go-pool-server/vardiff"
)

var log = logging.Logger("stratum")

type Server struct {
	Options  *config.Options
	Listener net.Listener

	DaemonManager       *daemonManager.DaemonManager
	VarDiff             *vardiff.VarDiff
	JobManager          *jobManager.JobManager
	StratumClients      map[uint64]*Client
	SubscriptionCounter *SubscriptionCounter
	BanningManager      *banningManager.BanningManager

	rebroadcastTicker *time.Ticker
	tickerReset       chan struct{}
}

func NewStratumServer(options *config.Options, jm *jobManager.JobManager, bm *banningManager.BanningManager) *Server {
	return &Server{
		Options:             options,
		BanningManager:      bm,
		SubscriptionCounter: NewSubscriptionCounter(),

		JobManager:     jm,
		StratumClients: make(map[uint64]*Client),
	}
}

func (ss *Server) Init() (portStarted []int) {
	if ss.Options.Banning != nil {
		ss.BanningManager.Init()
	}

	for port, options := range ss.Options.Ports {
		var err error
		if options.TLS != nil {
			ss.Listener, err = tls.Listen("tcp", ":"+strconv.Itoa(port), options.TLS.ToTLSConfig())
		} else {
			ss.Listener, _ = net.Listen("tcp", ":"+strconv.Itoa(port))
		}

		if err != nil {
			log.Error(err)
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
		ss.tickerReset = make(chan struct{})
		ss.rebroadcastTicker = time.NewTicker(time.Duration(ss.Options.JobRebroadcastTimeout) * time.Second)
		defer ss.rebroadcastTicker.Stop()
		for {
			select {
			case <-ss.tickerReset:
				ss.rebroadcastTicker = time.NewTicker(time.Duration(ss.Options.JobRebroadcastTimeout) * time.Second)
			case <-ss.rebroadcastTicker.C:
				ss.BroadcastMiningJobs(ss.JobManager.CurrentJob.GetJobParams())
			}
		}
	}()

	go func() {
		for {
			conn, err := ss.Listener.Accept()
			if err != nil {
				log.Error(err)
				continue
			}

			if conn != nil {
				log.Info("new conn from ", conn.RemoteAddr().String())
				go ss.HandleNewClient(conn)
			}
		}
	}()

	return portStarted
}

// HandleNewClient converts the conn to an underlying client instance and finally return its unique subscriptionID
func (ss *Server) HandleNewClient(socket net.Conn) []byte {
	subscriptionID := ss.SubscriptionCounter.Next()
	client := NewStratumClient(subscriptionID, socket, ss.Options, ss.JobManager, ss.BanningManager)
	ss.StratumClients[binary.LittleEndian.Uint64(subscriptionID)] = client
	// client.connected

	go func() {
		for {
			<-client.SocketClosedEvent
			log.Warn("a client socket closed")
			ss.RemoveStratumClientBySubscriptionId(subscriptionID)
			// client.disconnected
		}
	}()

	client.Init()

	return subscriptionID
}

func (ss *Server) BroadcastMiningJobs(jobParams []interface{}) {
	log.Info("broadcasting job params")
	for clientId := range ss.StratumClients {
		ss.StratumClients[clientId].SendMiningJob(jobParams)
	}
	ss.tickerReset <- struct{}{}
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
