package daemon

import "github.com/multica-ai/multica/server/pkg/agent"

// usesCanonicalResidentChatRuntime is true when full chat should enter the
// canonical tryCanonicalChatBackend path (agent×runtime slot, one long-lived
// resident process across channel/DM/thread surfaces, never force-fresh).
// Only actual conversational deliveries qualify; issue executions stay
// one-shot. ChatSessionID is retired and never participates in this decision.
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
	if !isChatLikeTask(task) {
		return false
	}
	switch provider {
	case "grok":
		return usesPersistentGrokChatRuntime(provider, task)
	case "pi":
		return usesPersistentPiChatRuntime(provider, task)
	case "cursor", "opencode", "kiro", "codex", "claude":
		profile, err := taskExecutionProfile(task)
		return err == nil && profile == executionProfileFull
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

// newCanonicalKiroResidentBackend builds the resident Kiro ACP adapter for the
// agent×runtime pool. Uses session/load (not session/resume). Close is
// mandatory on eviction and unhealthy release.
func newCanonicalKiroResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewKiroACPBackend(cfg)
	return backend, backend.Close, nil
}

// newCanonicalCodexResidentBackend builds the resident Codex app-server adapter
// for the agent×runtime pool. Close is mandatory on eviction and unhealthy release.
func newCanonicalCodexResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewCodexAppServerBackend(cfg)
	return backend, backend.Close, nil
}

// newCanonicalClaudeResidentBackend builds the resident raw Claude stream-json
// adapter. It intentionally avoids claude-agent-acp and gates busy input on
// provider-observed safe boundaries.
func newCanonicalClaudeResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewClaudeStreamJSONBackend(cfg)
	return backend, backend.Close, nil
}
