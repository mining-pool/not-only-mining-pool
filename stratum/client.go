package stratum

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"github.com/node-standalone-pool/go-pool-server/banningManager"
	"github.com/node-standalone-pool/go-pool-server/config"
	"github.com/node-standalone-pool/go-pool-server/daemonManager"
	"github.com/node-standalone-pool/go-pool-server/jobManager"
	"github.com/node-standalone-pool/go-pool-server/utils"
	"github.com/node-standalone-pool/go-pool-server/vardiff"
	"io"
	"log"
	"math/big"
	"net"
	"strconv"
	"time"
)

type Client struct {
	SubscriptionId []byte
	Options        *config.Options
	RemoteAddress  net.Addr
	Socket         net.Conn
	LastActivity   time.Time
	Shares         *Shares

	IsAuthorized           bool
	SubscriptionBeforeAuth bool

	ExtraNonce1 []byte

	VarDiff *vardiff.VarDiff

	WorkerName string
	WorkerPass string

	PendingDifficulty  *big.Float
	CurrentDifficulty  *big.Float
	PreviousDifficulty *big.Float

	BanningManager    *banningManager.BanningManager
	JobManager        *jobManager.JobManager
	SocketClosedEvent chan struct{}
}

func NewStratumClient(subscriptionId []byte, socket net.Conn, options *config.Options, jm *jobManager.JobManager, bm *banningManager.BanningManager) *Client {
	var varDiff *vardiff.VarDiff
	if options.Ports[socket.LocalAddr().(*net.TCPAddr).Port] != nil && options.Ports[socket.LocalAddr().(*net.TCPAddr).Port].VarDiff != nil {
		varDiff = vardiff.NewVarDiff(options.Ports[socket.LocalAddr().(*net.TCPAddr).Port].VarDiff)
	}

	return &Client{
		SubscriptionId:    subscriptionId,
		PendingDifficulty: big.NewFloat(0),
		Options:           options,
		RemoteAddress:     socket.RemoteAddr(),
		Socket:            socket,
		LastActivity:      time.Now(),
		Shares: &Shares{
			Valid:   0,
			Invalid: 0,
		},
		IsAuthorized:           false,
		SubscriptionBeforeAuth: false,
		ExtraNonce1:            jm.ExtraNonce1Generator.GetExtraNonce1(),

		VarDiff:        varDiff,
		JobManager:     jm,
		BanningManager: bm,
	}
}

func (sc *Client) ShouldBan(shareValid bool) bool {
	if shareValid {
		sc.Shares.Valid++
	} else {
		sc.Shares.Invalid++
		if sc.Shares.TotalShares() >= sc.Options.Banning.CheckThreshold {
			if sc.Shares.BadPercent() < sc.Options.Banning.InvalidPercent {
				sc.Shares.Reset()
			} else {
				//sc.TriggerBanEvent <- strconv.FormatUint(sc.Shares.Invalid, 10) + " out of the last " + strconv.FormatUint(sc.Shares.TotalShares(), 10) + " shares were invalid"
				log.Println(strconv.FormatUint(sc.Shares.Invalid, 10) + " out of the last " + strconv.FormatUint(sc.Shares.TotalShares(), 10) + " shares were invalid")
				sc.BanningManager.AddBannedIP(sc.RemoteAddress.String())
				log.Println("closed socket", sc.WorkerName, " due to shares bad percent reached the banning invalid percent threshold")
				sc.SocketClosedEvent <- struct{}{}
				sc.Socket.Close()
				return true
			}
		}
	}

	return false
}

func (sc *Client) Init() {
	sc.SetupSocket()
}

func (sc *Client) HandleMessage(message *daemonManager.JsonRpcRequest) {
	switch message.Method {
	case "mining.subscribe":
		sc.HandleSubscribe(message)
		break
	case "mining.authorize":
		sc.HandleAuthorize(message, true)
		break
	case "mining.submit":
		sc.LastActivity = time.Now()
		sc.HandleSubmit(message)
		break
	case "mining.get_transactions":
		sc.SendJson(&daemonManager.JsonRpcResponse{
			Id:     0,
			Result: nil,
			Error:  nil, // TODO: Support this
		})
		break
	default:
		log.Println("unknownStratumMethod", string(utils.Jsonify(message)))
		break
	}
}

func (sc *Client) HandleSubscribe(message *daemonManager.JsonRpcRequest) {
	if !sc.IsAuthorized {
		sc.SubscriptionBeforeAuth = true
	}

	extraNonce2Size := sc.JobManager.ExtraNonce2Size

	// TODO
	var err error
	if err != nil {
		sc.SendJson(&daemonManager.JsonRpcResponse{
			Id:     message.Id,
			Result: nil,
			Error: &daemonManager.JsonRpcError{
				Code:    20,
				Message: err.Error(),
			},
		})

		return
	}

	sc.SendJson(&daemonManager.JsonRpcResponse{
		Id: message.Id,
		Result: utils.Jsonify([]interface{}{
			[][]string{
				{"mining.set_difficulty", strconv.FormatUint(binary.LittleEndian.Uint64(sc.SubscriptionId), 10)},
				{"mining.notify", strconv.FormatUint(binary.LittleEndian.Uint64(sc.SubscriptionId), 10)},
			},
			hex.EncodeToString(sc.ExtraNonce1),
			extraNonce2Size,
		}),
		Error: nil,
	})
}

func (sc *Client) HandleAuthorize(message *daemonManager.JsonRpcRequest, replyToSocket bool) {
	sc.WorkerName = string(message.Params[0])
	sc.WorkerPass = string(message.Params[1])

	authorized, disconnect, err := sc.AuthorizeFn(sc.RemoteAddress, sc.Socket.LocalAddr().(*net.TCPAddr).Port, sc.WorkerName, sc.WorkerPass)
	sc.IsAuthorized = err == nil && authorized

	if replyToSocket {
		if sc.IsAuthorized {
			sc.SendJson(&daemonManager.JsonRpcResponse{
				Id:     message.Id,
				Result: utils.Jsonify(sc.IsAuthorized),
				Error:  nil,
			})
		} else {
			sc.SendJson(&daemonManager.JsonRpcResponse{
				Id:     message.Id,
				Result: utils.Jsonify(sc.IsAuthorized),
				Error: &daemonManager.JsonRpcError{
					Code:    20,
					Message: string(utils.Jsonify(err)),
				},
			})
		}
	}

	if disconnect {
		log.Println("closed socket", sc.WorkerName, "due to failed to authorize the miner")
		sc.Socket.Close()
		sc.SocketClosedEvent <- struct{}{}
	}

	// the init Diff for miners
	log.Println("sending init difficulty:", sc.Options.Ports[sc.Socket.LocalAddr().(*net.TCPAddr).Port].Diff)
	sc.SendDifficulty(big.NewFloat(sc.Options.Ports[sc.Socket.LocalAddr().(*net.TCPAddr).Port].Diff))
	sc.SendMiningJob(sc.JobManager.CurrentJob.GetJobParams())
}

// TODO: Can be DIY
func (sc *Client) AuthorizeFn(ip net.Addr, port int, workerName string, password string) (authorized bool, disconnect bool, err error) {
	log.Println("Authorize " + workerName + ":" + password + "@" + ip.String())
	return true, false, nil
}

func (sc *Client) HandleSubmit(message *daemonManager.JsonRpcRequest) {
	if !sc.IsAuthorized {
		sc.SendJson(&daemonManager.JsonRpcResponse{
			Id:     message.Id,
			Result: nil,
			Error: &daemonManager.JsonRpcError{
				Code:    24,
				Message: "unauthorized worker",
			},
		})
		sc.ShouldBan(false) // TODO: implement banning
	}

	if sc.ExtraNonce1 == nil {
		sc.SendJson(&daemonManager.JsonRpcResponse{
			Id:     message.Id,
			Result: nil,
			Error: &daemonManager.JsonRpcError{
				Code:    25,
				Message: "not subscribed",
			},
		})
		sc.ShouldBan(false)
	}

	_, _, errParams := sc.JobManager.ProcessShare(
		utils.RawJsonToString(message.Params[1]),
		sc.PreviousDifficulty,
		sc.CurrentDifficulty,
		sc.ExtraNonce1,
		utils.RawJsonToString(message.Params[2]),
		utils.RawJsonToString(message.Params[3]),
		utils.RawJsonToString(message.Params[4]),
		sc.RemoteAddress,
		sc.Socket.LocalAddr().(*net.TCPAddr).Port,
		utils.RawJsonToString(message.Params[0]),
	)

	if errParams != nil && errParams.Code == 23 {
		// warn the miner with current diff
		log.Println("Code23: sending ", string(utils.Jsonify([]json.RawMessage{utils.Jsonify(sc.CurrentDifficulty)})))
		f, _ := sc.CurrentDifficulty.Float64()
		sc.SendJson(&daemonManager.JsonRpcRequest{
			Id:     nil,
			Method: "mining.set_difficulty",
			Params: []json.RawMessage{utils.Jsonify(f)},
		})
	}

	// vardiff
	if sc.VarDiff != nil {
		diff, _ := sc.CurrentDifficulty.Float64()
		if nextDiff := sc.VarDiff.CalcNextDiff(diff); nextDiff != diff && nextDiff != 0 {
			sc.EnqueueNextDifficulty(nextDiff)
		}
	}

	if sc.PendingDifficulty != nil && sc.PendingDifficulty.Cmp(big.NewFloat(0)) != 0 {
		diff := sc.PendingDifficulty
		log.Println("sending new difficulty:", diff)
		ok := sc.SendDifficulty(diff)
		sc.PendingDifficulty = nil
		if ok {
			//difficultyChanged
			// -> difficultyUpdate client.workerName, diff
			displayDiff, _ := diff.Float64()
			log.Println("Difficulty update to diff:", displayDiff, "&workerName:", sc.WorkerName)
		}
	}

	sc.ShouldBan(true)
}

func (sc *Client) SendJson(jsonRpcs ...daemonManager.JsonRpc) {
	response := make([]byte, 0)
	for i := range jsonRpcs {
		response = append(response, jsonRpcs[i].Json()...)
		response = append(response, '\n')
	}

	_, _ = sc.Socket.Write(response)
}

func (sc *Client) SendSubscriptionFirstResponse() {

}

func (sc *Client) SetupSocket() {
	sc.BanningManager.CheckBan(sc.RemoteAddress.String())
	once := true

	go func() {
		r := bufio.NewReader(sc.Socket)
		for {
			select {
			case <-sc.SocketClosedEvent:
				return
			default:
				raw, err := r.ReadBytes('\n')
				if err != nil {
					if err == io.EOF {
						sc.SocketClosedEvent <- struct{}{}
						return
					}
					e, ok := err.(net.Error)

					if !ok {
						log.Println("failed to ready bytes from socket due to non-network error:", err)
						return
					}

					if ok && e.Timeout() {
						log.Println("socket is timeout:", err)
						return
					}

					if ok && e.Temporary() {
						log.Println("failed to ready bytes from socket due to temporary error:", err)
						continue
					}

					log.Println("failed to ready bytes from socket:", err)
					return
				}

				if len(raw) > 10240 {
					//socketFlooded
					log.Println("Flooding message from", sc.GetLabel(), ":", string(raw))
					sc.Socket.Close()
					sc.SocketClosedEvent <- struct{}{}
					return
				}

				if len(raw) == 0 {
					continue
				}

				var message daemonManager.JsonRpcRequest
				err = json.Unmarshal(raw, &message)
				if err != nil {
					if !sc.Options.TCPProxyProtocol {
						log.Println("Malformed message from", sc.GetLabel(), ":", string(raw))
						sc.Socket.Close()
						sc.SocketClosedEvent <- struct{}{}
					}

					return
				}

				if once && sc.Options.TCPProxyProtocol {
					once = false
					if bytes.HasPrefix(raw, []byte("PROXY")) {
						sc.RemoteAddress, err = net.ResolveTCPAddr("tcp", string(bytes.Split(raw, []byte(" "))[2]))
						if err != nil {
							log.Println("failed to resolve tcp addr behind proxy:", err)
						}
					} else {
						log.Println("Client IP detection failed, tcpProxyProtocol is enabled yet did not receive proxy protocol message, instead got data:", raw)
					}
				}

				sc.BanningManager.CheckBan(sc.RemoteAddress.String())

				if &message != nil {
					sc.HandleMessage(&message)
				}
			}
		}
	}()
}

func (sc *Client) GetLabel() string {
	if sc.WorkerName != "" {
		return sc.WorkerName + " [" + sc.RemoteAddress.String() + "]"
	} else {
		return "(unauthorized)" + " [" + sc.RemoteAddress.String() + "]"
	}
}

func (sc *Client) EnqueueNextDifficulty(nextDiff float64) bool {
	log.Println("EnqueueNextDifficulty:", nextDiff)
	sc.PendingDifficulty = big.NewFloat(nextDiff)
	return true
}

func (sc *Client) SendDifficulty(diff *big.Float) bool {
	if diff == nil {
		log.Fatal("trying to send empty diff!")
	}
	if sc.CurrentDifficulty != nil && diff.Cmp(sc.CurrentDifficulty) == 0 {
		return false
	}

	sc.PreviousDifficulty = sc.CurrentDifficulty
	sc.CurrentDifficulty = diff

	f, _ := diff.Float64()
	sc.SendJson(&daemonManager.JsonRpcRequest{
		Id:     0,
		Method: "mining.set_difficulty",
		Params: []json.RawMessage{utils.Jsonify(f)},
	})

	return true
}

func (sc *Client) SendMiningJob(jobParams []interface{}) {
	log.Println("sending job:", string(utils.Jsonify(jobParams)))
	lastActivityAgo := time.Now().Sub(sc.LastActivity)
	if lastActivityAgo > time.Duration(sc.Options.ConnectionTimeout)*time.Second {
		log.Println("closed socket", sc.WorkerName, "due to activity timeout")
		sc.Socket.Close()
		sc.SocketClosedEvent <- struct{}{}
		return
	}

	if sc.PendingDifficulty != nil && sc.PendingDifficulty.Cmp(big.NewFloat(0)) != 0 {
		diff := sc.PendingDifficulty
		ok := sc.SendDifficulty(diff)
		sc.PendingDifficulty = nil
		if ok {
			//difficultyChanged
			// -> difficultyUpdate client.workerName, diff
			displayDiff, _ := diff.Float64()
			log.Println("Difficulty update to diff:", displayDiff, "&workerName:", sc.WorkerName)
		}
	}

	params := make([]json.RawMessage, len(jobParams))
	for i := range jobParams {
		params[i] = utils.Jsonify(jobParams[i])
	}

	sc.SendJson(&daemonManager.JsonRpcRequest{
		Id:     0,
		Method: "mining.notify",
		Params: params,
	})
}

func (sc *Client) ManuallyAuthClient(username, password string) {
	sc.HandleAuthorize(&daemonManager.JsonRpcRequest{
		Id:     1,
		Method: "",
		Params: []json.RawMessage{utils.Jsonify(username), utils.Jsonify(password)},
	}, false)
}

func (sc *Client) ManuallySetValues(otherClient *Client) {
	sc.ExtraNonce1 = otherClient.ExtraNonce1
	sc.PreviousDifficulty = otherClient.PreviousDifficulty
	sc.CurrentDifficulty = otherClient.CurrentDifficulty
}
