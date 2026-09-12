package stratum

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"net"
	"strconv"
	"sync"
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/mining-pool/not-only-mining-pool/bans"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/jobs"
	"github.com/mining-pool/not-only-mining-pool/storage"
	"github.com/mining-pool/not-only-mining-pool/vardiff"
)

var log = logging.Logger("stratum")

type Server struct {
	Options  *config.Options
	Listener net.Listener

	DaemonManager       *daemons.DaemonManager
	VarDiff             *vardiff.VarDiff
	JobManager          *jobs.JobManager
	StratumClients      map[uint64]*Client
	clientsMu           sync.RWMutex // guards StratumClients
	SubscriptionCounter *SubscriptionCounter
	BanningManager      *bans.BanningManager

	// Engine, when non-nil, drives an alternative mining model (e.g. ethash);
	// the server then skips the Bitcoin/GBT rebroadcast loop.
	Engine engine.Engine
	// DB persists engine-mode shares (the GBT path persists via JobManager.Storage).
	DB *storage.DB

	rebroadcastTicker *time.Ticker
}

func NewStratumServer(options *config.Options, jm *jobs.JobManager, bm *bans.BanningManager) *Server {
	return &Server{
		Options:             options,
		BanningManager:      bm,
		SubscriptionCounter: NewSubscriptionCounter(),

		JobManager:     jm,
		StratumClients: make(map[uint64]*Client),
	}
}

func (ss *Server) Init() (portStarted []int) {
	var listeners []net.Listener
	if ss.Options.Banning != nil {
		ss.BanningManager.Init()
	}

	for port, options := range ss.Options.Ports {
		var err error
		if provider, ok := ss.Engine.(interface {
			Listen(int, *config.PortOptions) (net.Listener, error)
		}); ok {
			ss.Listener, err = provider.Listen(port, options)
		} else if options.TLS != nil {
			ss.Listener, err = tls.Listen("tcp", ":"+strconv.Itoa(port), options.TLS.ToTLSConfig())
		} else {
			ss.Listener, err = net.Listen("tcp", ":"+strconv.Itoa(port))
		}

		if err != nil {
			log.Error(err)
			continue
		}

		portStarted = append(portStarted, port)
		listeners = append(listeners, ss.Listener)
		//if len(portStarted) == len(ss.Options.Ports) {
		//	// emit started
		//}
	}

	if len(portStarted) == 0 {
		log.Panic("No port listened")
	}

	if ss.Engine != nil {
		// Engine-driven work: the engine watches the node (events first, polling
		// only as fallback) and calls back on every new-work signal.
		go func() {
			defer log.Warn("engine watcher stopped")
			if err := ss.Engine.Watch(ss.BroadcastEngineWork); err != nil {
				log.Error("engine watch stopped: ", err)
			}
		}()
	} else {
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
	}

	for _, listener := range listeners {
		go func(listener net.Listener) {
			for {
				conn, err := listener.Accept()
				if err != nil {
					log.Error(err)
					return
				}

				if conn != nil {
					log.Info("new conn from ", conn.RemoteAddr().String())
					go ss.HandleNewClient(conn)
				}
			}
		}(listener)
	}

	return portStarted
}

// HandleNewClient converts the conn to an underlying client instance and finally return its unique subscriptionID
func (ss *Server) HandleNewClient(socket net.Conn) []byte {
	subscriptionID := ss.SubscriptionCounter.Next()
	client := NewStratumClient(subscriptionID, socket, ss.Options, ss.JobManager, ss.BanningManager)
	client.Engine = ss.Engine
	if provider, ok := ss.Engine.(interface {
		ClientEngine(int, *config.PortOptions) engine.Engine
	}); ok {
		port := socket.LocalAddr().(*net.TCPAddr).Port
		client.Engine = provider.ClientEngine(port, ss.Options.Ports[port])
	}
	client.DB = ss.DB
	ss.clientsMu.Lock()
	ss.StratumClients[binary.LittleEndian.Uint64(subscriptionID)] = client
	ss.clientsMu.Unlock()
	// client.connected

	go func() {
		<-client.SocketClosedEvent
		log.Warn("a client socket closed")
		ss.RemoveStratumClientBySubscriptionId(subscriptionID)
	}()

	client.Init()

	return subscriptionID
}

// snapshotClients returns the current clients under a read lock, so broadcasts
// can iterate (and do network I/O per client) without holding the lock while
// other goroutines add/remove entries.
func (ss *Server) snapshotClients() []*Client {
	ss.clientsMu.RLock()
	defer ss.clientsMu.RUnlock()
	clients := make([]*Client, 0, len(ss.StratumClients))
	for _, c := range ss.StratumClients {
		clients = append(clients, c)
	}
	return clients
}

func (ss *Server) clientBySubscriptionId(subscriptionId []byte) *Client {
	ss.clientsMu.RLock()
	defer ss.clientsMu.RUnlock()
	return ss.StratumClients[binary.LittleEndian.Uint64(subscriptionId)]
}

func (ss *Server) BroadcastCurrentMiningJob(jobParams []interface{}) {
	log.Info("broadcasting job params")
	for _, c := range ss.snapshotClients() {
		c.SendMiningJob(jobParams)
	}
}

// BroadcastEngineWork pushes fresh engine work to every authorized client, each
// at its own difficulty/target.
func (ss *Server) BroadcastEngineWork() {
	log.Info("broadcasting engine work")
	for _, c := range ss.snapshotClients() {
		c.broadcastEngineWork()
	}
}

func (ss *Server) RemoveStratumClientBySubscriptionId(subscriptionId []byte) {
	ss.clientsMu.Lock()
	delete(ss.StratumClients, binary.LittleEndian.Uint64(subscriptionId))
	ss.clientsMu.Unlock()
}

func (ss *Server) ManuallyAddStratumClient(client *Client) {
	subscriptionId := ss.HandleNewClient(client.Socket)
	if subscriptionId != nil {
		if c := ss.clientBySubscriptionId(subscriptionId); c != nil {
			c.ManuallyAuthClient(client.WorkerName, client.WorkerPass)
			c.ManuallySetValues(client)
		}
	}
}
