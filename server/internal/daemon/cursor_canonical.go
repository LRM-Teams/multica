package daemon

import "github.com/multica-ai/multica/server/pkg/agent"

// usesCanonicalResidentChatRuntime is true when full chat should enter the
// D6-1b tryCanonicalChatBackend path (agent×runtime + ChatSessionID context
// key). Covers Grok, Pi, and Cursor. Issue / empty ChatSession stays one-shot.
// This is not a third ChatSession-keyed pool — only the admission gate into
// the shared canonical entry (#702 PR B).
func usesCanonicalResidentChatRuntime(provider string, task Task) bool {
	switch provider {
	case "grok":
		return usesPersistentGrokChatRuntime(provider, task)
	case "pi":
		return usesPersistentPiChatRuntime(provider, task)
	case "cursor":
		profile, err := taskExecutionProfile(task)
		return err == nil && profile == executionProfileFull && task.ChatSessionID != ""
	default:
		return false
	}
}

// newCanonicalCursorResidentBackend builds the D4/D6 resident Cursor ACP
// adapter for the agent×runtime pool. Close is mandatory on eviction /
// unhealthy release and on ContextKey rotate (same table as Grok/Pi).
func newCanonicalCursorResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewCursorACPBackend(cfg)
	return backend, backend.Close, nil
}
