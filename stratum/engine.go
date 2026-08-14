package stratum

import (
	"encoding/json"
	"math/big"
	"net"

	"github.com/mining-pool/not-only-mining-pool/daemons"
	"github.com/mining-pool/not-only-mining-pool/engine"
	"github.com/mining-pool/not-only-mining-pool/utils"
)

// engineSession adapts a *Client to engine.Session so a pluggable engine can
// read per-connection state and push notifications without importing stratum.
type engineSession struct{ sc *Client }

func (s engineSession) ExtraNonce1() []byte { return s.sc.ExtraNonce1 }

func (s engineSession) Difficulty() float64 {
	if s.sc.CurrentDifficulty == nil {
		return 0
	}
	d, _ := s.sc.CurrentDifficulty.Float64()
	return d
}

func (s engineSession) WorkerName() string { return s.sc.WorkerName }

func (s engineSession) RemoteAddr() net.Addr { return s.sc.RemoteAddress }

// Send pushes a stratum notification. An empty method means "reply as a bare
// JSON-RPC result" (the ethproxy convention for pushing work with id:0);
// otherwise it is sent as a JSON-RPC request/notification. Engines declaring
// ObjectParams() send params[0] as a bare JSON object (the CryptoNote "job"
// push shape) instead of an array.
func (s engineSession) Send(method string, params []interface{}) error {
	if method == "" {
		s.sc.SendJsonRPC(&daemons.JsonRpcResponse{
			Id:     0,
			Result: utils.Jsonify(params),
		})
		return nil
	}
	var raw json.RawMessage
	if s.sc.wantsObjectParams() && len(params) == 1 {
		raw = utils.Jsonify(params[0])
	} else {
		raw = daemons.MarshalParams(params...)
	}
	s.sc.SendJsonRPC(&daemons.JsonRpcRequest{Id: nil, Method: method, Params: raw})
	return nil
}

// diffJobber is an optional engine capability: build the work package for a
// specific share difficulty (ethash-style engines put the target inside the
// work, so it is per-connection). Engines that don't implement it fall back to
// the global JobNotification.
type diffJobber interface {
	JobParamsForDifficulty(diff float64) []interface{}
}

// notifyMethoder is an optional engine capability: the JSON-RPC method used to
// push work. Empty (default) means ethproxy-style bare result with id:0;
// kawpow-style engines return "mining.notify".
type notifyMethoder interface {
	NotifyMethod() string
}

// targetSetter is an optional engine capability: a message pushed BEFORE each
// work notification (zcash-style dialects send mining.set_target ahead of
// mining.notify because the target is not part of the notify params).
type targetSetter interface {
	TargetParams(diff float64) (method string, params []interface{})
}

// objectParamser is an optional engine capability: push notifications carry a
// single JSON OBJECT as params instead of an array (the CryptoNote/XMRig
// dialect, e.g. {"method":"job","params":{...}}).
type objectParamser interface {
	ObjectParams() bool
}

// wantsObjectParams reports whether the active engine speaks the object-params
// (CryptoNote/XMRig) dialect.
func (sc *Client) wantsObjectParams() bool {
	op, ok := sc.Engine.(objectParamser)
	return ok && op.ObjectParams()
}

// portDefaultDiff returns the starting difficulty configured for this client's port.
func (sc *Client) portDefaultDiff() float64 {
	if p := sc.Options.Ports[sc.Socket.LocalAddr().(*net.TCPAddr).Port]; p != nil {
		return p.Diff
	}
	return 1
}

// engineDiff returns this client's current share difficulty (falling back to
// the port's starting diff before the first retarget).
func (sc *Client) engineDiff() float64 {
	if sc.CurrentDifficulty != nil {
		d, _ := sc.CurrentDifficulty.Float64()
		return d
	}
	return sc.portDefaultDiff()
}

// engineJobParams returns the work package to hand this client, using the
// per-connection difficulty when the engine supports it.
func (sc *Client) engineJobParams() []interface{} {
	if ej, ok := sc.Engine.(diffJobber); ok {
		return ej.JobParamsForDifficulty(sc.engineDiff())
	}
	_, params := sc.Engine.JobNotification(true)
	return params
}

// sendEngineWork pushes the current work package to the miner, using the
// engine's notify method (or an id:0 bare result for ethproxy engines). If the
// engine sets targets out-of-band (zcash-style), that message goes first.
func (sc *Client) sendEngineWork() {
	params := sc.engineJobParams()
	if params == nil {
		return
	}
	if ts, ok := sc.Engine.(targetSetter); ok {
		if method, tParams := ts.TargetParams(sc.engineDiff()); method != "" {
			_ = engineSession{sc}.Send(method, tParams)
		}
	}
	method := ""
	if nm, ok := sc.Engine.(notifyMethoder); ok {
		method = nm.NotifyMethod()
	}
	_ = engineSession{sc}.Send(method, params)
}

// handleEngineMessage routes stratum messages to the active engine. It supports
// the ethproxy dialect (eth_submitLogin / eth_getWork / eth_submitWork /
// eth_submitHashrate) and tolerates the generic mining.* method names.
func (sc *Client) handleEngineMessage(message *daemons.JsonRpcRequest) {
	sess := engineSession{sc}

	switch message.Method {
	case "mining.subscribe":
		// dialect handshake only: reply with the engine's subscribe result and
		// remember the assigned extranonce; work is pushed after authorize.
		result, en1, _ := sc.Engine.OnSubscribe(sess, rawParamsToIface(message.Params))
		if en1 != nil {
			sc.ExtraNonce1 = en1
		}
		sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(result)})

	case "eth_submitLogin", "mining.authorize":
		if sc.ExtraNonce1 == nil { // ethproxy skips mining.subscribe
			_, en1, _ := sc.Engine.OnSubscribe(sess, rawParamsToIface(message.Params))
			sc.ExtraNonce1 = en1
		}
		if arr := message.ParamsArray(); len(arr) > 0 {
			sc.WorkerName = utils.RawJsonToString(arr[0])
		}
		sc.IsAuthorized = true
		if sc.CurrentDifficulty == nil {
			sc.CurrentDifficulty = big.NewFloat(sc.portDefaultDiff())
		}
		sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(true)})
		sc.sendEngineWork()

	case "login":
		// CryptoNote/XMRig: one call does subscribe+authorize and the reply
		// carries the first job, so no work push follows.
		params := rawParamsToIface(message.Params)
		result, en1, _ := sc.Engine.OnSubscribe(sess, params)
		if en1 != nil {
			sc.ExtraNonce1 = en1
		}
		if len(params) == 1 {
			if obj, ok := params[0].(map[string]interface{}); ok {
				if l, ok := obj["login"].(string); ok {
					sc.WorkerName = l
				}
			}
		}
		sc.IsAuthorized = true
		if sc.CurrentDifficulty == nil {
			sc.CurrentDifficulty = big.NewFloat(sc.portDefaultDiff())
		}
		sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(result)})

	case "getjob":
		params := sc.engineJobParams()
		if sc.wantsObjectParams() && len(params) == 1 {
			sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(params[0])})
		} else {
			sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(params)})
		}

	case "keepalived":
		sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(map[string]string{"status": "KEEPALIVED"})})

	case "eth_getWork":
		sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(sc.engineJobParams())})

	case "eth_submitWork", "mining.submit", "submit":
		if !sc.IsAuthorized {
			sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(false),
				Error: &daemons.JsonRpcError{Code: 24, Message: "unauthorized worker"}})
			sc.ShouldBan(false)
			return
		}

		share := sc.Engine.OnSubmit(sess, rawParamsToIface(message.Params))
		valid := share.ErrorCode == 0

		// TODO(engine): persist share to storage for stats/payments (the engine
		// already submits solved blocks to the node). Requires a storage handle
		// on the engine-mode client; tracked in docs/PLUGGABLE_ENGINES_zh.md.
		if valid {
			log.Info(sc.WorkerName, " submitted a valid engine share, diff=", share.Diff)
			sc.applyEngineVarDiff()
		} else {
			log.Warn(sc.WorkerName, "'s engine share invalid: ", share.ErrorCode.String())
		}

		if sc.ShouldBan(valid) {
			return
		}
		if !valid {
			sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(false),
				Error: &daemons.JsonRpcError{Code: int(share.ErrorCode), Message: share.ErrorCode.String()}})
			return
		}
		if sc.wantsObjectParams() {
			// CryptoNote miners expect {"status":"OK"} instead of a boolean
			sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(map[string]string{"status": "OK"})})
		} else {
			sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(true)})
		}

	case "eth_submitHashrate", "mining.extranonce.subscribe":
		sc.SendJsonRPC(&daemons.JsonRpcResponse{Id: message.Id, Result: utils.Jsonify(true)})

	default:
		log.Warn("unknown engine stratum method: ", string(utils.Jsonify(message)))
	}
}

// applyEngineVarDiff retargets the client difficulty and re-pushes work when the
// new target differs (ethash carries the target inside the work package).
func (sc *Client) applyEngineVarDiff() {
	if sc.VarDiff == nil || sc.CurrentDifficulty == nil {
		return
	}
	cur, _ := sc.CurrentDifficulty.Float64()
	next := sc.VarDiff.CalcNextDiff(cur)
	if next != 0 && next != cur {
		sc.CurrentDifficulty = big.NewFloat(next)
		log.Info("ethash vardiff retarget ", sc.WorkerName, " -> ", next)
		sc.sendEngineWork()
	}
}

// rawParamsToIface decodes request params for engine dispatch: array params
// become []interface{} element-wise; OBJECT params (CryptoNote login/submit)
// are wrapped as a single-element slice holding the decoded map.
func rawParamsToIface(raw json.RawMessage) []interface{} {
	if len(raw) == 0 {
		return nil
	}
	var arr []interface{}
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return []interface{}{obj}
	}
	return nil
}

// compile-time assertion that the adapter satisfies engine.Session.
var _ engine.Session = engineSession{}
