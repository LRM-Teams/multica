package daemon

import "github.com/multica-ai/multica/server/pkg/agent"

// usesCanonicalResidentChatRuntime is true when full chat should enter the
// canonical tryCanonicalChatBackend path (agent×runtime slot, one long-lived
// resident process across channel/DM/thread surfaces, never force-fresh).
// Covers Grok, Pi, and Cursor. Issue tasks and chats without a ChatSessionID
// stay one-shot.
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

// newCanonicalCursorResidentBackend builds the resident Cursor ACP adapter
// for the agent×runtime pool. Close is mandatory on eviction and unhealthy
// release, matching Grok/Pi's resident adapters.
func newCanonicalCursorResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewCursorACPBackend(cfg)
	return backend, backend.Close, nil
}
