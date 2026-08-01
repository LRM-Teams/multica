package daemon

import "github.com/multica-ai/multica/server/pkg/agent"

// usesCanonicalResidentChatRuntime is true when full chat should enter the
// canonical tryCanonicalChatBackend path (agent×runtime slot, one long-lived
// resident process across channel/DM/thread surfaces, never force-fresh).
// Issue tasks and chats without a ChatSessionID stay one-shot.
//
// Provider membership comes from agent.Capabilities.CanonicalResident
// (task #47). The switch below answers a DIFFERENT question — "does this
// task's shape qualify for the resident path for this provider?" — and must
// not be collapsed into the capability table (Parker: same name ≠ same meaning).
// See TestCanonicalResidentProviderListsStayInSync.
func usesCanonicalResidentChatRuntime(provider string, task Task) bool {
	if !agent.Capabilities(provider).CanonicalResident {
		return false
	}
	switch provider {
	case "grok":
		return usesPersistentGrokChatRuntime(provider, task)
	case "pi":
		return usesPersistentPiChatRuntime(provider, task)
	case "cursor", "opencode":
		profile, err := taskExecutionProfile(task)
		return err == nil && profile == executionProfileFull && task.ChatSessionID != ""
	default:
		// Capability says resident but no task-shape adapter — fail closed.
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
