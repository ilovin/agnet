package ws

import "encoding/json"

func decodeParams(m map[string]any, v any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

type ImageData struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

type AgentCreateParams struct {
	Name      string   `json:"name"`
	Provider  string   `json:"provider"`
	Cmd       string   `json:"cmd"`
	WorkDir   string   `json:"workDir"`
	SessionID string   `json:"sessionId"`
	Model     string   `json:"model"`
	Args      []string `json:"args"`
}

type AgentStopParams struct {
	AgentID string `json:"agentId"`
}

type AgentRestartParams struct {
	AgentID        string `json:"agentId"`
	Model          string `json:"model"`
	Provider       string `json:"provider"`
	PermissionMode string `json:"permissionMode"`
}

type AgentAttachParams struct {
	PID float64 `json:"pid"`
}

type SessionAttachParams struct {
	AgentID   string   `json:"agentId"`
	SessionID string   `json:"sessionId"`
	PID       float64  `json:"pid"`
	Name      string   `json:"name"`
	Provider  string   `json:"provider"`
	Cmd       string   `json:"cmd"`
	WorkDir   string   `json:"workDir"`
	Model     string   `json:"model"`
	Args      []string `json:"args"`
}

type ConversationSendParams struct {
	AgentID string      `json:"agentId"`
	Message string      `json:"message"`
	Raw     bool        `json:"raw"`
	Images  []ImageData `json:"images"`
}

type ConversationKeyParams struct {
	AgentID string  `json:"agentId"`
	Key     string  `json:"key"`
	Repeat  float64 `json:"repeat"`
}

type ConversationHistoryParams struct {
	AgentID string  `json:"agentId"`
	Cursor  float64 `json:"cursor"`
	Limit   float64 `json:"limit"`
	Before  float64 `json:"before"`
}

type ConversationImageParams struct {
	Path string `json:"path"`
}

type ConversationPermissionResponseParams struct {
	AgentID      string         `json:"agentId"`
	RequestID    string         `json:"requestId"`
	Behavior     string         `json:"behavior"`
	Message      string         `json:"message"`
	UpdatedInput map[string]any `json:"updatedInput"`
}

type ConversationClearParams struct {
	AgentID string `json:"agentId"`
	NodeID  string `json:"nodeId"`
}

type AgentRenameParams struct {
	AgentID string `json:"agentId"`
	Name    string `json:"name"`
}

type AgentRemoveParams struct {
	AgentID string `json:"agentId"`
}

type ProviderListParams struct {
	AgentID string `json:"agentId"`
}

type ProviderSwitchParams struct {
	ProviderID string `json:"providerId"`
	AgentID    string `json:"agentId"`
}

type ProviderAddParams struct {
	Name      string `json:"name"`
	BaseURL   string `json:"baseUrl"`
	AuthToken string `json:"authToken"`
	Model     string `json:"model"`
}
