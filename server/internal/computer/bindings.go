package computer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// bindingsFile is the machine-wide file that persists the Computer's Workspace
// Execution Bindings. It lives directly under the machine root (never under a
// workspace slug), so renaming a Workspace never moves or duplicates local
// state and identity is never derived from a slug (#2489, #2490).
const bindingsFile = "bindings.json"

// WorkspaceBinding is one explicit Workspace Execution Binding: the Computer
// is authorized to execute work for the immutable workspace_id using the
// revocable execution credential. The slug is a display/selector only.
type WorkspaceBinding struct {
	WorkspaceID   string    `json:"workspace_id"`
	WorkspaceSlug string    `json:"workspace_slug,omitempty"`
	ComputerID    string    `json:"computer_id"`
	Credential    string    `json:"credential,omitempty"`
	AcceptedAt    time.Time `json:"accepted_at,omitempty"`
	Active        bool      `json:"active,omitempty"`
}

// BindingsStore persists the machine-wide set of Workspace Bindings with
// atomic, permission-restricted writes. It is keyed by the immutable
// workspace_id, so re-adding or repairing a Binding never duplicates it.
type BindingsStore struct {
	root string
}

// NewBindingsStore returns a store rooted at the machine-wide state dir.
func NewBindingsStore(root string) *BindingsStore {
	return &BindingsStore{root: root}
}

func (s *BindingsStore) path() string { return filepath.Join(s.root, bindingsFile) }

// All returns the persisted bindings, ordered by workspace_id for stability.
func (s *BindingsStore) All() ([]WorkspaceBinding, error) {
	data, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []WorkspaceBinding
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse bindings: %w", err)
	}
	return out, nil
}

// Get returns the binding for workspaceID, if present and active.
func (s *BindingsStore) Get(workspaceID string) (WorkspaceBinding, bool, error) {
	all, err := s.All()
	if err != nil {
		return WorkspaceBinding{}, false, err
	}
	for _, b := range all {
		if b.WorkspaceID == workspaceID && b.Active {
			return b, true, nil
		}
	}
	return WorkspaceBinding{}, false, nil
}

// AddOrRepair upserts one binding keyed by the immutable workspace_id: a
// repeat for the same Workspace repairs/refreshes it without duplication
// (#2490). It never derives local state from the slug.
func (s *BindingsStore) AddOrRepair(b WorkspaceBinding) error {
	all, _ := s.All()
	replaced := false
	for i := range all {
		if all[i].WorkspaceID == b.WorkspaceID {
			all[i] = b
			replaced = true
			break
		}
	}
	if !replaced {
		all = append(all, b)
	}
	return s.write(all)
}

// Remove deletes exactly one Binding by workspace_id (see #2493 for the Web
// removal contract; local removal here applies the same single-Workspace scope).
func (s *BindingsStore) Remove(workspaceID string) error {
	all, err := s.All()
	if err != nil {
		return err
	}
	out := all[:0]
	for _, b := range all {
		if b.WorkspaceID != workspaceID {
			out = append(out, b)
		}
	}
	return s.write(out)
}

// AllActive returns only the valid (active) bindings — the set `computer
// start` restores for every stored Workspace (#2490).
func (s *BindingsStore) AllActive() ([]WorkspaceBinding, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	var out []WorkspaceBinding
	for _, b := range all {
		if b.Active {
			out = append(out, b)
		}
	}
	return out, nil
}

// Install validates the request against current bindings and then applies the
// change atomically. On validation failure nothing is written, so a failed
// additive setup leaves existing Bindings and the running Computer unchanged
// (#2490).
func (s *BindingsStore) Install(req BindingRequest, b WorkspaceBinding) error {
	current, err := s.All()
	if err != nil {
		return err
	}
	if _, err := ValidateCreate(req, current); err != nil {
		return err
	}
	return s.AddOrRepair(b)
}

// Remove validates the removal request and then applies it atomically.
func (s *BindingsStore) RemoveValidated(req BindingRequest, workspaceID string) error {
	current, err := s.All()
	if err != nil {
		return err
	}
	if err := ValidateRemove(req, current); err != nil {
		return err
	}
	return s.Remove(workspaceID)
}

// write persists the full binding set atomically with 0600 permissions.
func (s *BindingsStore) write(bindings []WorkspaceBinding) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("create bindings directory: %w", err)
	}
	data, err := json.MarshalIndent(bindings, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".bindings-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, s.path()); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
