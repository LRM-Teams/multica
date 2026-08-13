package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const mixedRunActivityOutboxRel = ".multica/mixed-run-activity-outbox.json"

type mixedRunActivityOutboxRecord struct {
	WorkspaceID string                                     `json:"workspace_id"`
	Payload     protocol.MixedRunActivityTransitionPayload `json:"payload"`
}

type mixedRunActivityOutboxState struct {
	Entries []mixedRunActivityOutboxRecord `json:"entries"`
}

type mixedRunActivityOutbox struct {
	path string

	mu      sync.Mutex
	loaded  bool
	entries []mixedRunActivityOutboxRecord
}

func newMixedRunActivityOutbox(workspacesRoot string) *mixedRunActivityOutbox {
	workspacesRoot = strings.TrimSpace(workspacesRoot)
	if workspacesRoot == "" {
		return nil
	}
	return &mixedRunActivityOutbox{path: filepath.Join(workspacesRoot, filepath.FromSlash(mixedRunActivityOutboxRel))}
}

func mixedRunActivityTransitionKey(runID, transitionID string) string {
	return strings.TrimSpace(runID) + "\x00" + strings.TrimSpace(transitionID)
}

func sameMixedRunActivityRecord(left, right mixedRunActivityOutboxRecord) bool {
	return left.WorkspaceID == right.WorkspaceID && left.Payload == right.Payload
}

func (o *mixedRunActivityOutbox) loadLocked() error {
	if o.loaded {
		return nil
	}
	data, err := os.ReadFile(o.path)
	if errors.Is(err, os.ErrNotExist) {
		o.loaded = true
		o.entries = nil
		return nil
	}
	if err != nil {
		return fmt.Errorf("read mixed-run activity outbox: %w", err)
	}
	var state mixedRunActivityOutboxState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode mixed-run activity outbox: %w", err)
	}
	seen := make(map[string]mixedRunActivityOutboxRecord, len(state.Entries))
	entries := make([]mixedRunActivityOutboxRecord, 0, len(state.Entries))
	for _, record := range state.Entries {
		if strings.TrimSpace(record.WorkspaceID) == "" {
			return errors.New("decode mixed-run activity outbox: workspace identity is empty")
		}
		if err := record.Payload.Validate(); err != nil {
			return fmt.Errorf("decode mixed-run activity outbox: %w", err)
		}
		key := mixedRunActivityTransitionKey(record.Payload.RunID, record.Payload.TransitionID)
		if prior, ok := seen[key]; ok {
			if !sameMixedRunActivityRecord(prior, record) {
				return errors.New("decode mixed-run activity outbox: transition identity has colliding payloads")
			}
			continue
		}
		seen[key] = record
		entries = append(entries, record)
	}
	o.entries = entries
	o.loaded = true
	return nil
}

func (o *mixedRunActivityOutbox) enqueue(workspaceID string, payload protocol.MixedRunActivityTransitionPayload) error {
	if o == nil {
		return errors.New("mixed-run activity outbox is unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return errors.New("mixed-run activity workspace identity is empty")
	}
	if err := payload.Validate(); err != nil {
		return err
	}
	record := mixedRunActivityOutboxRecord{WorkspaceID: workspaceID, Payload: payload}
	key := mixedRunActivityTransitionKey(payload.RunID, payload.TransitionID)
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.loadLocked(); err != nil {
		return err
	}
	for _, existing := range o.entries {
		if mixedRunActivityTransitionKey(existing.Payload.RunID, existing.Payload.TransitionID) != key {
			continue
		}
		if sameMixedRunActivityRecord(existing, record) {
			return nil
		}
		return errors.New("mixed-run activity transition id conflicts with a different local payload")
	}
	o.entries = append(o.entries, record)
	if err := o.saveLocked(); err != nil {
		o.entries = o.entries[:len(o.entries)-1]
		return err
	}
	return nil
}

func (o *mixedRunActivityOutbox) acknowledge(ack protocol.MixedRunActivityTransitionAckPayload) error {
	if o == nil {
		return errors.New("mixed-run activity outbox is unavailable")
	}
	if err := ack.Validate(); err != nil {
		return err
	}
	key := mixedRunActivityTransitionKey(ack.RunID, ack.TransitionID)
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.loadLocked(); err != nil {
		return err
	}
	for index, record := range o.entries {
		if mixedRunActivityTransitionKey(record.Payload.RunID, record.Payload.TransitionID) != key {
			continue
		}
		prior := append([]mixedRunActivityOutboxRecord(nil), o.entries...)
		o.entries = append(o.entries[:index], o.entries[index+1:]...)
		if err := o.saveLocked(); err != nil {
			o.entries = prior
			return err
		}
		return nil
	}
	return nil
}

func (o *mixedRunActivityOutbox) pending(workspaceID string) ([]protocol.MixedRunActivityTransitionPayload, error) {
	if o == nil {
		return nil, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.loadLocked(); err != nil {
		return nil, err
	}
	pending := make([]protocol.MixedRunActivityTransitionPayload, 0, len(o.entries))
	for _, record := range o.entries {
		if record.WorkspaceID == workspaceID {
			pending = append(pending, record.Payload)
		}
	}
	return pending, nil
}

func (o *mixedRunActivityOutbox) saveLocked() error {
	data, err := json.Marshal(mixedRunActivityOutboxState{Entries: o.entries})
	if err != nil {
		return fmt.Errorf("encode mixed-run activity outbox: %w", err)
	}
	dir := filepath.Dir(o.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create mixed-run activity outbox directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".mixed-run-activity-outbox-*")
	if err != nil {
		return fmt.Errorf("create mixed-run activity outbox temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write mixed-run activity outbox: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync mixed-run activity outbox: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("protect mixed-run activity outbox: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close mixed-run activity outbox: %w", err)
	}
	if err := os.Rename(tmpPath, o.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish mixed-run activity outbox: %w", err)
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open mixed-run activity outbox directory: %w", err)
	}
	defer dirFile.Close()
	if err := dirFile.Sync(); err != nil {
		return fmt.Errorf("sync mixed-run activity outbox directory: %w", err)
	}
	return nil
}
