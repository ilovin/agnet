package agent

import (
	"sync"
	"time"
)

// PermissionRequest represents a permission request from Claude Code
// This matches the Companion permission system structure
type PermissionRequest struct {
	RequestID             string                 `json:"request_id"`
	ToolName              string                 `json:"tool_name"`
	DisplayName           string                 `json:"display_name,omitempty"`
	Title                 string                 `json:"title,omitempty"`
	Description           string                 `json:"description,omitempty"`
	Input                 map[string]interface{} `json:"input"`
	PermissionSuggestions []PermissionSuggestion `json:"permission_suggestions,omitempty"`
	ToolUseID             string                 `json:"tool_use_id,omitempty"`
	AgentID               string                 `json:"agent_id,omitempty"`
	BlockedPath           string                 `json:"blocked_path,omitempty"`
	DecisionReason        string                 `json:"decision_reason,omitempty"`
	AIValidation          *AIValidationInfo      `json:"ai_validation,omitempty"`
	Timestamp             int64                  `json:"timestamp"`
}

// PermissionSuggestion represents a suggested permission action
type PermissionSuggestion struct {
	Type        string   `json:"type"` // "setMode", "addRules", "replaceRules", "addDirectories"
	Mode        string   `json:"mode,omitempty"`
	Rules       []Rule   `json:"rules,omitempty"`
	Directories []string `json:"directories,omitempty"`
	Destination string   `json:"destination,omitempty"` // "session" or "always"
}

// Rule represents a permission rule
type Rule struct {
	Tool       string `json:"tool,omitempty"`
	RuleType   string `json:"ruleType,omitempty"`
	RuleContent string `json:"ruleContent,omitempty"`
}

// AIValidationInfo represents AI safety analysis for a permission request
type AIValidationInfo struct {
	Verdict string `json:"verdict"` // "safe", "dangerous", "uncertain"
	Reason  string `json:"reason"`
}

// PermissionResponse represents a user's response to a permission request
type PermissionResponse struct {
	RequestID          string                 `json:"request_id"`
	Behavior           string                 `json:"behavior"` // "allow" or "deny"
	Message            string                 `json:"message,omitempty"`
	UpdatedInput       map[string]interface{} `json:"updated_input,omitempty"`
	UpdatedPermissions []PermissionSuggestion `json:"updated_permissions,omitempty"`
}

// PermissionManager manages pending permission requests for an agent
type PermissionManager struct {
	mu              sync.RWMutex
	pendingRequests map[string]*PermissionRequest // request_id -> request
}

// NewPermissionManager creates a new permission manager
func NewPermissionManager() *PermissionManager {
	return &PermissionManager{
		pendingRequests: make(map[string]*PermissionRequest),
	}
}

// AddRequest adds a new permission request
func (pm *PermissionManager) AddRequest(req *PermissionRequest) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if req.Timestamp == 0 {
		req.Timestamp = time.Now().UnixMilli()
	}
	pm.pendingRequests[req.RequestID] = req
}

// GetRequest retrieves a permission request by ID
func (pm *PermissionManager) GetRequest(requestID string) *PermissionRequest {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.pendingRequests[requestID]
}

// RemoveRequest removes a permission request
func (pm *PermissionManager) RemoveRequest(requestID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.pendingRequests, requestID)
}

// GetPendingRequests returns all pending permission requests
func (pm *PermissionManager) GetPendingRequests() []*PermissionRequest {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	requests := make([]*PermissionRequest, 0, len(pm.pendingRequests))
	for _, req := range pm.pendingRequests {
		requests = append(requests, req)
	}
	return requests
}

// HasPendingRequests returns true if there are any pending permission requests
func (pm *PermissionManager) HasPendingRequests() bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.pendingRequests) > 0
}

// HandleResponse processes a permission response
func (pm *PermissionManager) HandleResponse(resp *PermissionResponse) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	_, exists := pm.pendingRequests[resp.RequestID]
	if exists {
		delete(pm.pendingRequests, resp.RequestID)
		return true
	}
	return false
}
