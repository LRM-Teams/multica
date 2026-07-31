package daemon

import "github.com/multica-ai/multica/server/pkg/agent"

// newCanonicalOpenCodeResidentBackend builds the resident OpenCode adapter
// for the agent×runtime pool. Close is mandatory on eviction and unhealthy
// release, matching Cursor/Grok/Pi's resident adapters.
func newCanonicalOpenCodeResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
	backend := agent.NewOpenCodeServeBackend(cfg)
	return backend, backend.Close, nil
}
