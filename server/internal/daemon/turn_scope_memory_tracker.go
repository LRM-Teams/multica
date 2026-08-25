package daemon

import (
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// turnScopeMemoryTracker remembers which user/project/channel memory scopes
// have already been injected into a provider session (or resident process),
// so repeated wakes do not re-pay the same pre-message tokens.
type turnScopeMemoryTracker struct {
	mu     sync.Mutex
	loaded map[string]map[string]struct{} // sessionKey -> scope keys
}

func newTurnScopeMemoryTracker() *turnScopeMemoryTracker {
	return &turnScopeMemoryTracker{loaded: map[string]map[string]struct{}{}}
}

func issueTurnScopeSessionKey(agentID, runtimeID, providerSessionID string) string {
	return strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID) + "\x00" + strings.TrimSpace(providerSessionID)
}

func residentTurnScopeSessionKey(agentID, runtimeID string) string {
	return strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID) + "\x00" + "resident"
}

func turnScopeMemoryKey(memory execenv.MemoryContextForEnv) (string, bool) {
	scope := strings.ToLower(strings.TrimSpace(memory.Scope))
	switch scope {
	case "user", "member":
		scope = "user"
	case "project", "channel":
		// keep
	default:
		// workspace/graph/agent and unknowns are not session-deduped here.
		return "", false
	}
	subject := strings.TrimSpace(memory.SubjectID)
	if subject == "" {
		return "", false
	}
	return scope + ":" + subject, true
}

// selectForInject returns memories that should enter this wake's pre-message.
// freshSession (no prior provider session) always keeps every row. Otherwise
// user/project/channel rows already marked on sessionKey are dropped.
// Graph/workspace rows always pass through.
func (t *turnScopeMemoryTracker) selectForInject(sessionKey string, memories []execenv.MemoryContextForEnv, freshSession bool) []execenv.MemoryContextForEnv {
	if t == nil || len(memories) == 0 || freshSession {
		return memories
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return memories
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	set := t.loaded[sessionKey]
	if len(set) == 0 {
		return memories
	}

	out := make([]execenv.MemoryContextForEnv, 0, len(memories))
	for _, memory := range memories {
		key, dedupe := turnScopeMemoryKey(memory)
		if dedupe {
			if _, seen := set[key]; seen {
				continue
			}
		}
		out = append(out, memory)
	}
	return out
}

// markInjected records user/project/channel keys that were placed into context
// for sessionKey so later wakes can skip them.
func (t *turnScopeMemoryTracker) markInjected(sessionKey string, memories []execenv.MemoryContextForEnv) {
	if t == nil || len(memories) == 0 {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.loaded == nil {
		t.loaded = map[string]map[string]struct{}{}
	}
	set := t.loaded[sessionKey]
	if set == nil {
		set = map[string]struct{}{}
		t.loaded[sessionKey] = set
	}
	for _, memory := range memories {
		if key, ok := turnScopeMemoryKey(memory); ok {
			set[key] = struct{}{}
		}
	}
}

// clearSession drops the loaded-scope set for one session key.
func (t *turnScopeMemoryTracker) clearSession(sessionKey string) {
	if t == nil {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	t.mu.Lock()
	delete(t.loaded, sessionKey)
	t.mu.Unlock()
}

// clearResident drops the resident continuum tracker for an agent×runtime.
func (t *turnScopeMemoryTracker) clearResident(agentID, runtimeID string) {
	t.clearSession(residentTurnScopeSessionKey(agentID, runtimeID))
}
