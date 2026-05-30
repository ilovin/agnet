package provider

// SessionLifecyclePolicy controls watcher-driven session switch behavior.
type SessionLifecyclePolicy struct {
	ClearPersistedEvents bool
	ResetEventBuffer     bool
	ReloadHistory        bool
	EmitClearedEvent     bool
	EmitClearedSessionID bool
}

var defaultSessionLifecycle = map[string]SessionLifecyclePolicy{
	"claude": {
		ClearPersistedEvents: true,
		ResetEventBuffer:     true,
		ReloadHistory:        false,
		EmitClearedEvent:     true,
		EmitClearedSessionID: false,
	},
	"codex": {
		ClearPersistedEvents: true,
		ResetEventBuffer:     true,
		ReloadHistory:        false,
		EmitClearedEvent:     true,
		EmitClearedSessionID: false,
	},
	"opencode": {
		ClearPersistedEvents: true,
		ResetEventBuffer:     true,
		ReloadHistory:        true,
		EmitClearedEvent:     true,
		EmitClearedSessionID: true,
	},
	"hermes": {
		ClearPersistedEvents: true,
		ResetEventBuffer:     true,
		ReloadHistory:        true,
		EmitClearedEvent:     true,
		EmitClearedSessionID: true,
	},
}

func SessionLifecyclePolicyFor(name string) SessionLifecyclePolicy {
	if p, ok := defaultSessionLifecycle[name]; ok {
		return p
	}
	return SessionLifecyclePolicy{
		ClearPersistedEvents: true,
		ResetEventBuffer:     true,
		ReloadHistory:        false,
		EmitClearedEvent:     true,
		EmitClearedSessionID: false,
	}
}
