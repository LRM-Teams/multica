package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// agentRuntimeSessionStore is the machine-local provider session identity
// the next start will resume. Restart keeps the stored id; session reset
// and full reset clear it so the next start cannot resume a stale session.
type agentRuntimeSessionStore struct {
	root string
	mu   sync.Mutex
}

func newAgentRuntimeSessionStore(workspacesRoot string) *agentRuntimeSessionStore {
	return &agentRuntimeSessionStore{root: strings.TrimSpace(workspacesRoot)}
}

func (s *agentRuntimeSessionStore) Get(agentID, runtimeID string) (string, error) {
	if s == nil || s.root == "" {
		return "", errors.New("session identity store is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := os.ReadFile(s.path(agentID, runtimeID))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func (s *agentRuntimeSessionStore) Put(agentID, runtimeID, sessionID string) error {
	if s == nil || s.root == "" {
		return errors.New("session identity store is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(agentID, runtimeID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(path, []byte(sessionID+"\n"), 0o600)
}

func (s *agentRuntimeSessionStore) Invalidate(commandID, agentID, runtimeID string) error {
	if s == nil {
		return errors.New("session identity store is not configured")
	}
	_ = commandID
	return s.Put(agentID, runtimeID, "")
}

func (s *agentRuntimeSessionStore) path(agentID, runtimeID string) string {
	return filepath.Join(s.root, ".multica", "runtime-sessions", strings.TrimSpace(agentID), strings.TrimSpace(runtimeID))
}
