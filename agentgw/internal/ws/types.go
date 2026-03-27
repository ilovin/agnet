package ws

type RPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type RPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type RPCEvent struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

func okResp(id any, result any) RPCResponse {
	return RPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id any, code int, msg string) RPCResponse {
	return RPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}}
}

func newEvent(method string, params any) RPCEvent {
	return RPCEvent{JSONRPC: "2.0", Method: method, Params: params}
}
