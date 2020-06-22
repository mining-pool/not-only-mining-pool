package stratum

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	logging "github.com/ipfs/go-log"
	"net"
	"strconv"
	"time"

	"github.com/mining-pool/not-only-mining-pool/banningManager"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemonManager"
	"github.com/mining-pool/not-only-mining-pool/jobManager"
	"github.com/mining-pool/not-only-mining-pool/vardiff"
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
			ss.Listener, err = net.Listen("tcp", ":"+strconv.Itoa(port))
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
		log.Panic("No port listened")
	}

	go func() {
		var id string
		var txs []byte
		ss.rebroadcastTicker = time.NewTicker(time.Duration(ss.Options.JobRebroadcastTimeout) * time.Second)
		defer log.Warn("broadcaster stopped")
		defer ss.rebroadcastTicker.Stop()
		for {
			<-ss.rebroadcastTicker.C
			go ss.BroadcastCurrentMiningJob(ss.JobManager.CurrentJob.GetJobParams(
				id != ss.JobManager.CurrentJob.JobId || !bytes.Equal(txs, ss.JobManager.CurrentJob.TransactionData),
			))

			id = ss.JobManager.CurrentJob.JobId
			txs = ss.JobManager.CurrentJob.TransactionData
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

func (ss *Server) BroadcastCurrentMiningJob(jobParams []interface{}) {
	log.Info("broadcasting job params")
	for clientId := range ss.StratumClients {
		ss.StratumClients[clientId].SendMiningJob(jobParams)
	}
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
