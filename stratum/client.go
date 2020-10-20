package stratum

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"strconv"
	"time"

	"github.com/mining-pool/not-only-mining-pool/types"

	"github.com/mining-pool/not-only-mining-pool/bans"
	"github.com/mining-pool/not-only-mining-pool/config"
	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/jobs"
	"github.com/mining-pool/not-only-mining-pool/utils"
	"github.com/mining-pool/not-only-mining-pool/vardiff"
)

type Client struct {
	SubscriptionId []byte
	Options        *config.Options
	RemoteAddress  net.Addr

	Socket      net.Conn
	SocketBufIO *bufio.ReadWriter

	LastActivity time.Time
	Shares       *Shares

	IsAuthorized           bool
	SubscriptionBeforeAuth bool

	ExtraNonce1 []byte

	VarDiff *vardiff.VarDiff

	WorkerName string
	WorkerPass string

	PendingDifficulty  *big.Float
	CurrentDifficulty  *big.Float
	PreviousDifficulty *big.Float

	BanningManager    *bans.BanningManager
	JobManager        *jobs.JobManager
	SocketClosedEvent chan struct{}
}

func NewStratumClient(subscriptionId []byte, socket net.Conn, options *config.Options, jm *jobs.JobManager, bm *bans.BanningManager) *Client {
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
		SocketBufIO:       bufio.NewReadWriter(bufio.NewReader(socket), bufio.NewWriter(socket)),
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
				// sc.TriggerBanEvent <- strconv.FormatUint(sc.Shares.Invalid, 10) + " out of the last " + strconv.FormatUint(sc.Shares.TotalShares(), 10) + " shares were invalid"
				log.Info(strconv.FormatUint(sc.Shares.Invalid, 10) + " out of the last " + strconv.FormatUint(sc.Shares.TotalShares(), 10) + " shares were invalid")
				sc.BanningManager.AddBannedIP(sc.RemoteAddress.String())
				log.Warn("closed socket", sc.WorkerName, " due to shares bad percent reached the banning invalid percent threshold")
				sc.SocketClosedEvent <- struct{}{}
				_ = sc.Socket.Close()
				return true
			}
		}
	}

	return false
}

func (sc *Client) Init() {
	sc.SetupSocket()
}

func (sc *Client) HandleMessage(message *daemons.JsonRpcRequest) {
	switch message.Method {
	case "mining.subscribe":
		sc.HandleSubscribe(message)
	case "mining.authorize":
		sc.HandleAuthorize(message, true)
	case "mining.submit":
		sc.LastActivity = time.Now()
		sc.HandleSubmit(message)
	case "mining.get_transactions":
		sc.SendJsonRPC(&daemons.JsonRpcResponse{
			Id:     0,
			Result: nil,
			Error:  nil, // TODO: Support this
		})
	default:
		log.Warn("unknown stratum method: ", string(utils.Jsonify(message)))
	}
}

func (sc *Client) HandleSubscribe(message *daemons.JsonRpcRequest) {
	log.Info("handling subscribe")
	if !sc.IsAuthorized {
		sc.SubscriptionBeforeAuth = true
	}

	extraNonce2Size := sc.JobManager.ExtraNonce2Size

	// TODO
	//var err error
	//if err != nil {
	//	sc.SendJson(&daemons.JsonRpcResponse{
	//		Id:     message.Id,
	//		Result: nil,
	//		ErrorCode: &daemons.JsonRpcError{
	//			Code:    20,
	//			Message: err.ErrorCode(),
	//		},
	//	})
	//
	//	return
	//}

	sc.SendJsonRPC(&daemons.JsonRpcResponse{
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

func (sc *Client) HandleAuthorize(message *daemons.JsonRpcRequest, replyToSocket bool) {
	log.Info("handling authorize")

	sc.WorkerName = string(message.Params[0])
	sc.WorkerPass = string(message.Params[1])

	authorized, disconnect, err := sc.AuthorizeFn(sc.RemoteAddress, sc.Socket.LocalAddr().(*net.TCPAddr).Port, sc.WorkerName, sc.WorkerPass)
	sc.IsAuthorized = err == nil && authorized

	if replyToSocket {
		if sc.IsAuthorized {
			sc.SendJsonRPC(&daemons.JsonRpcResponse{
				Id:     message.Id,
				Result: utils.Jsonify(sc.IsAuthorized),
				Error:  nil,
			})
		} else {
			sc.SendJsonRPC(&daemons.JsonRpcResponse{
				Id:     message.Id,
				Result: utils.Jsonify(sc.IsAuthorized),
				Error: &daemons.JsonRpcError{
					Code:    20,
					Message: string(utils.Jsonify(err)),
				},
			})
		}
	}

	if disconnect {
		log.Warn("closed socket", sc.WorkerName, "due to failed to authorize the miner")
		_ = sc.Socket.Close()
		sc.SocketClosedEvent <- struct{}{}
	}

	// the init Diff for miners
	log.Info("sending init difficulty: ", sc.Options.Ports[sc.Socket.LocalAddr().(*net.TCPAddr).Port].Diff)
	sc.SendDifficulty(big.NewFloat(sc.Options.Ports[sc.Socket.LocalAddr().(*net.TCPAddr).Port].Diff))
	sc.SendMiningJob(sc.JobManager.CurrentJob.GetJobParams(true))
}

// TODO: Can be DIY
func (sc *Client) AuthorizeFn(ip net.Addr, port int, workerName string, password string) (authorized bool, disconnect bool, err error) {
	log.Info("Authorize " + workerName + ": " + password + "@" + ip.String())
	return true, false, nil
}

func (sc *Client) HandleSubmit(message *daemons.JsonRpcRequest) {
	/* Avoid hash flood */
	if !sc.IsAuthorized {
		sc.SendJsonRPC(&daemons.JsonRpcResponse{
			Id:     message.Id,
			Result: nil,
			Error: &daemons.JsonRpcError{
				Code:    24,
				Message: "unauthorized worker",
			},
		})
		sc.ShouldBan(false)
		return
	}

	if sc.ExtraNonce1 == nil {
		sc.SendJsonRPC(&daemons.JsonRpcResponse{
			Id:     message.Id,
			Result: nil,
			Error: &daemons.JsonRpcError{
				Code:    25,
				Message: "not subscribed",
			},
		})
		sc.ShouldBan(false)
		return
	}

	share := sc.JobManager.ProcessSubmit(
		utils.RawJsonToString(message.Params[1]),
		sc.PreviousDifficulty,
		sc.CurrentDifficulty,
		sc.ExtraNonce1,
		utils.RawJsonToString(message.Params[2]),
		utils.RawJsonToString(message.Params[3]),
		utils.RawJsonToString(message.Params[4]),
		sc.RemoteAddress,
		utils.RawJsonToString(message.Params[0]),
	)

	sc.JobManager.ProcessShare(share)

	if share.ErrorCode == types.ErrLowDiffShare {
		// warn the miner with current diff
		log.Error("Error on handling submit: sending new diff ", string(utils.Jsonify([]json.RawMessage{utils.Jsonify(sc.CurrentDifficulty)})), " to miner")
		f, _ := sc.CurrentDifficulty.Float64()
		sc.SendJsonRPC(&daemons.JsonRpcRequest{
			Id:     nil,
			Method: "mining.set_difficulty",
			Params: []json.RawMessage{utils.Jsonify(f)},
		})
	}

	if share.ErrorCode == types.ErrNTimeOutOfRange {
		sc.SendMiningJob(sc.JobManager.CurrentJob.GetJobParams(true))
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
		log.Info("sending new difficulty: ", diff)
		ok := sc.SendDifficulty(diff)
		sc.PendingDifficulty = nil
		if ok {
			// difficultyChanged
			// -> difficultyUpdate client.workerName, diff
			displayDiff, _ := diff.Float64()
			log.Info("Difficulty update to diff:", displayDiff, "&workerName:", sc.WorkerName)
		}
	}

	if sc.ShouldBan(share.ErrorCode == 0) {
		return
	}

	var errParams *daemons.JsonRpcError
	if share.ErrorCode != 0 {
		errParams = &daemons.JsonRpcError{
			Code:    int(share.ErrorCode),
			Message: share.ErrorCode.String(),
		}

		log.Error(sc.WorkerName, "'s share is invalid: ", errParams.Message)
		sc.SendJsonRPC(&daemons.JsonRpcResponse{
			Id:     message.Id,
			Result: utils.Jsonify(false),
			Error:  errParams,
		})
	}

	log.Info(sc.WorkerName, " submitted a valid share")
	sc.SendJsonRPC(&daemons.JsonRpcResponse{
		Id:     message.Id,
		Result: utils.Jsonify(true),
	})
}

func (sc *Client) SendJsonRPC(jsonRPCs daemons.JsonRpc) {
	raw := jsonRPCs.Json()

	message := make([]byte, 0, len(raw)+1)
	message = append(raw, '\n')
	_, err := sc.SocketBufIO.Write(message)
	if err != nil {
		log.Error("failed inputting", string(raw), err)
	}

	err = sc.SocketBufIO.Flush()
	if err != nil {
		log.Error("failed sending data", err)
	}

	log.Debug("sent raw bytes: ", string(raw))
}

func (sc *Client) SendSubscriptionFirstResponse() {
}

func (sc *Client) SetupSocket() {
	sc.BanningManager.CheckBan(sc.RemoteAddress.String())
	once := true

	go func() {
		for {
			select {
			case <-sc.SocketClosedEvent:
				return
			default:
				raw, err := sc.SocketBufIO.ReadBytes('\n')
				if err != nil {
					if err == io.EOF {
						sc.SocketClosedEvent <- struct{}{}
						return
					}
					e, ok := err.(net.Error)

					if !ok {
						log.Error("failed to ready bytes from socket due to non-network error:", err)
						return
					}

					if ok && e.Timeout() {
						log.Error("socket is timeout:", err)
						return
					}

					if ok && e.Temporary() {
						log.Error("failed to ready bytes from socket due to temporary error:", err)
						continue
					}

					log.Error("failed to ready bytes from socket:", err)
					return
				}

				if len(raw) > 10240 {
					// socketFlooded
					log.Warn("Flooding message from", sc.GetLabel(), ":", string(raw))
					_ = sc.Socket.Close()
					sc.SocketClosedEvent <- struct{}{}
					return
				}

				if len(raw) == 0 {
					continue
				}

				var message daemons.JsonRpcRequest
				err = json.Unmarshal(raw, &message)
				if err != nil {
					if !sc.Options.TCPProxyProtocol {
						log.Error("Malformed message from", sc.GetLabel(), ":", string(raw))
						_ = sc.Socket.Close()
						sc.SocketClosedEvent <- struct{}{}
					}

					return
				}

				if once && sc.Options.TCPProxyProtocol {
					once = false
					if bytes.HasPrefix(raw, []byte("PROXY")) {
						sc.RemoteAddress, err = net.ResolveTCPAddr("tcp", string(bytes.Split(raw, []byte(" "))[2]))
						if err != nil {
							log.Error("failed to resolve tcp addr behind proxy:", err)
						}
					} else {
						log.Error("Client IP detection failed, tcpProxyProtocol is enabled yet did not receive proxy protocol message, instead got data:", raw)
					}
				}

				sc.BanningManager.CheckBan(sc.RemoteAddress.String())

				if &message != nil {
					log.Debug("handling message: ", string(message.Json()))
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
	log.Info("Enqueue next difficulty:", nextDiff)
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
	sc.SendJsonRPC(&daemons.JsonRpcRequest{
		Id:     0,
		Method: "mining.set_difficulty",
		Params: []json.RawMessage{utils.Jsonify(f)},
	})

	return true
}

func (sc *Client) SendMiningJob(jobParams []interface{}) {
	log.Info("sending job: ", string(utils.Jsonify(jobParams)))

	lastActivityAgo := time.Since(sc.LastActivity)
	if lastActivityAgo > time.Duration(sc.Options.ConnectionTimeout)*time.Second {
		log.Info("closed socket", sc.WorkerName, "due to activity timeout")
		_ = sc.Socket.Close()
		sc.SocketClosedEvent <- struct{}{}
		return
	}

	if sc.PendingDifficulty != nil && sc.PendingDifficulty.Cmp(big.NewFloat(0)) != 0 {
		diff := sc.PendingDifficulty
		ok := sc.SendDifficulty(diff)
		sc.PendingDifficulty = nil
		if ok {
			// difficultyChanged
			// -> difficultyUpdate client.workerName, diff
			displayDiff, _ := diff.Float64()
			log.Info("Difficulty update to diff:", displayDiff, "&workerName:", sc.WorkerName)
		}
	}

	params := make([]json.RawMessage, len(jobParams))
	for i := range jobParams {
		params[i] = utils.Jsonify(jobParams[i])
	}

	sc.SendJsonRPC(&daemons.JsonRpcRequest{
		Id:     nil,
		Method: "mining.notify",
		Params: params,
	})
}

func (sc *Client) ManuallyAuthClient(username, password string) {
	sc.HandleAuthorize(&daemons.JsonRpcRequest{
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
