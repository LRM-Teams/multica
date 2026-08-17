package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// agentRuntimeSessionStore is the in-process resume cache for one DaemonCore.
//
// Raft 1.0.16 keeps the same fact in idleRestartSnapshots: the id last
// applied by agent:start / the live RuntimeSession. It is not a disk
// pointer and is not keyed by cwd. A new DaemonCore starts empty and
// waits for the next start payload.
//
// Restart keeps the cached id. Session reset and full reset clear it so
// the next start cannot resume a stale session.
type agentRuntimeSessionStore struct {
	mu       sync.Mutex
	sessions map[string]string
}

func newAgentRuntimeSessionStore(workspacesRoot string) *agentRuntimeSessionStore {
	// TODO(raft-session-resume): drop leftover disk files after v0.4.24-alpha.67
	// is no longer a supported direct self-upgrade source. Raft never stored
	// the resume id at workspaces/.multica/runtime-sessions/<agent>/<runtime>.
	if root := strings.TrimSpace(workspacesRoot); root != "" {
		_ = os.RemoveAll(filepath.Join(root, ".multica", "runtime-sessions"))
	}
	return &agentRuntimeSessionStore{sessions: make(map[string]string)}
}

func (s *agentRuntimeSessionStore) Get(agentID, runtimeID string) (string, error) {
	if s == nil {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sessionKey(agentID, runtimeID)], nil
}

func (s *agentRuntimeSessionStore) Put(agentID, runtimeID, sessionID string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]string)
	}
	key := sessionKey(agentID, runtimeID)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		delete(s.sessions, key)
		return nil
	}
	s.sessions[key] = sessionID
	return nil
}

func (s *agentRuntimeSessionStore) Invalidate(commandID, agentID, runtimeID string) error {
	_ = commandID
	return s.Put(agentID, runtimeID, "")
}

func sessionKey(agentID, runtimeID string) string {
	return strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(runtimeID)
}
