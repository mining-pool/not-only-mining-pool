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

type JsonRpcRequest struct {
	Id     interface{}       `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
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
