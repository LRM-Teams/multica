package daemon

import "github.com/multica-ai/multica/server/pkg/agent"

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
