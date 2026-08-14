package daemons

import "encoding/json"

type JsonRpc interface {
	GetJsonRpcId() int64
	Json() []byte
}

type JsonRpcResponse struct {
	Id     interface{}     `json:"id"` // be int64 or null
	Result json.RawMessage `json:"result,omitempty"`
	Error  *JsonRpcError   `json:"error,omitempty"`
}

func (j *JsonRpcResponse) GetJsonRpcId() int64 {
	if j.Id == nil {
		return 0
	}

	return j.Id.(int64)
}

func (j *JsonRpcResponse) Json() []byte {
	raw, _ := json.Marshal(j)
	return raw
}

// JsonRpcRequest holds params as raw JSON: bitcoin-family stratum uses array
// params, while CryptoNote miners (XMRig login/submit) send OBJECT params —
// both must survive parsing (an object used to kill the connection).
type JsonRpcRequest struct {
	Id     interface{}     `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ParamsArray interprets Params as a JSON array (the bitcoin stratum shape).
// It returns nil for object params or malformed input.
func (j *JsonRpcRequest) ParamsArray() []json.RawMessage {
	var arr []json.RawMessage
	if err := json.Unmarshal(j.Params, &arr); err != nil {
		return nil
	}
	return arr
}

// MarshalParams renders values as a JSON array for JsonRpcRequest.Params.
func MarshalParams(values ...interface{}) json.RawMessage {
	raw, _ := json.Marshal(values)
	return raw
}

func (j *JsonRpcRequest) GetJsonRpcId() int64 {
	if j.Id == nil {
		return 0
	}

	return j.Id.(int64)
}

func (j *JsonRpcRequest) Json() []byte {
	raw, _ := json.Marshal(j)
	return raw
}

type JsonRpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

//type Method string
//
//const (
//	MethodSubmitBlock Method = "getsubmitblock"
//	MethodGetBlockTemplate Method = "getblocktemplate"
//	 MethodGetBlock Method = "getblock"
//	MethodGetBalance Method = "getbalance"
//	MethodValidateAddress Method = "validateaddress"
//	)
