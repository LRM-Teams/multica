package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// agentLifecycleCommandLedger records the terminal outcome of a restart
// command so a replay of the same id is a no-op and a different payload
// on that id fails closed.
type agentLifecycleCommandLedger struct {
	root string
	mu   sync.Mutex
}

type agentLifecycleCommandRecord struct {
	CommandID string `json:"command_id"`
	Kind      string `json:"kind"`
}

func newAgentLifecycleCommandLedger(workspacesRoot string) *agentLifecycleCommandLedger {
	return &agentLifecycleCommandLedger{root: strings.TrimSpace(workspacesRoot)}
}

// Begin reports whether this command already completed. replay=true means
// the caller must skip destructive work. A different kind on the same id
// is a conflict.
func (l *agentLifecycleCommandLedger) Begin(commandID, kind string) (replay bool, err error) {
	if l == nil || l.root == "" {
		return false, nil
	}
	commandID = strings.TrimSpace(commandID)
	kind = strings.TrimSpace(kind)
	if commandID == "" || kind == "" {
		return false, errors.New("command id and kind are required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	path := l.path(commandID)
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var rec agentLifecycleCommandRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return false, err
	}
	if rec.Kind != kind {
		return false, fmt.Errorf("command %s already bound to %s", commandID, rec.Kind)
	}
	return true, nil
}

func (l *agentLifecycleCommandLedger) Commit(commandID, kind string) error {
	if l == nil || l.root == "" {
		return nil
	}
	commandID = strings.TrimSpace(commandID)
	kind = strings.TrimSpace(kind)
	if commandID == "" || kind == "" {
		return errors.New("command id and kind are required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	path := l.path(commandID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(agentLifecycleCommandRecord{CommandID: commandID, Kind: kind})
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

func (l *agentLifecycleCommandLedger) path(commandID string) string {
	return filepath.Join(l.root, ".multica", "lifecycle-commands", commandID+".json")
}

type agentLifecycleStartRequest struct {
	CommandID   string
	WorkspaceID string
	AgentID     string
	RuntimeID   string
	SessionID   string
}

type agentLifecycleStarter interface {
	Start(ctx context.Context, req agentLifecycleStartRequest) error
}
