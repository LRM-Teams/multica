package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

// bindingsFile is the machine-wide file that persists the Computer's Workspace
// Execution Bindings. It lives directly under the machine root (never under a
// workspace slug), so renaming a Workspace never moves or duplicates local
// state and identity is never derived from a slug (#2489, #2490).
const bindingsFile = "bindings.json"
const bindingsLockFile = "bindings.lock"

// WorkspaceBinding is one explicit Workspace Execution Binding: the Computer
// is authorized to execute work for the immutable workspace_id using the
// revocable execution credential. The slug is a display/selector only.
type WorkspaceBinding struct {
	Environment         string    `json:"environment,omitempty"`
	Origin              string    `json:"origin,omitempty"`
	WorkspaceID         string    `json:"workspace_id"`
	WorkspaceSlug       string    `json:"workspace_slug,omitempty"`
	ComputerID          string    `json:"computer_id"`
	UserID              string    `json:"-"`
	Credential          string    `json:"credential,omitempty"`
	CredentialExpiresAt time.Time `json:"credential_expires_at,omitempty"`
	AcceptedAt          time.Time `json:"accepted_at,omitempty"`
	Active              bool      `json:"active,omitempty"`
}

// BindingsStore persists the machine-wide set of Workspace Bindings with
// atomic, permission-restricted writes. It is keyed by (environment,
// workspace_id), so production and test databases may use the same UUID
// without overwriting each other's credentials.
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
	for i := range out {
		out[i] = normalizeWorkspaceBinding(out[i])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Environment == out[j].Environment {
			return out[i].WorkspaceID < out[j].WorkspaceID
		}
		return out[i].Environment < out[j].Environment
	})
	return out, nil
}

// Get returns the production binding for workspaceID. New Computer callers
// should use GetForEnvironment; this method keeps old on-disk/test callers on
// the only environment that existed before the environment axis was added.
func (s *BindingsStore) Get(workspaceID string) (WorkspaceBinding, bool, error) {
	return s.GetForEnvironment(string(cli.ServiceEnvironmentProduction), workspaceID)
}

// GetForEnvironment returns one active connection by its full local key.
func (s *BindingsStore) GetForEnvironment(environment, workspaceID string) (WorkspaceBinding, bool, error) {
	all, err := s.All()
	if err != nil {
		return WorkspaceBinding{}, false, err
	}
	environment = normalizeBindingEnvironment(environment)
	for _, b := range all {
		if b.Environment == environment && b.WorkspaceID == workspaceID && b.Active {
			return b, true, nil
		}
	}
	return WorkspaceBinding{}, false, nil
}

// AddOrRepair upserts one binding keyed by the immutable workspace_id: a
// repeat for the same Workspace repairs/refreshes it without duplication
// (#2490). It never derives local state from the slug.
func (s *BindingsStore) AddOrRepair(b WorkspaceBinding) error {
	return s.withMutationLock(func() error {
		all, err := s.All()
		if err != nil {
			return err
		}
		return s.addOrRepair(all, b)
	})
}

// Remove deletes one production connection. Environment-aware callers use
// RemoveForEnvironment so an identical test Workspace id remains untouched.
func (s *BindingsStore) Remove(workspaceID string) error {
	return s.RemoveForEnvironment(string(cli.ServiceEnvironmentProduction), workspaceID)
}

// RemoveForEnvironment deletes exactly one connection by its full local key.
func (s *BindingsStore) RemoveForEnvironment(environment, workspaceID string) error {
	return s.withMutationLock(func() error {
		all, err := s.All()
		if err != nil {
			return err
		}
		return s.remove(all, environment, workspaceID)
	})
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

// AllActiveForEnvironment returns the connections restored by the single
// resident for its current service stage. Connections from the other stage
// stay persisted but are not contacted by this resident generation.
func (s *BindingsStore) AllActiveForEnvironment(environment string) ([]WorkspaceBinding, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	environment = normalizeBindingEnvironment(environment)
	var out []WorkspaceBinding
	for _, b := range all {
		if b.Active && b.Environment == environment {
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
	return s.withMutationLock(func() error {
		current, err := s.All()
		if err != nil {
			return err
		}
		if _, err := ValidateCreate(req, current); err != nil {
			return err
		}
		return s.addOrRepair(current, b)
	})
}

// Remove validates the removal request and then applies it atomically.
func (s *BindingsStore) RemoveValidated(req BindingRequest, workspaceID string) error {
	return s.withMutationLock(func() error {
		current, err := s.All()
		if err != nil {
			return err
		}
		if err := ValidateRemove(req, current); err != nil {
			return err
		}
		return s.remove(current, string(cli.ServiceEnvironmentProduction), workspaceID)
	})
}

func (s *BindingsStore) withMutationLock(mutate func() error) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("create bindings directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(s.root, bindingsLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open bindings lock: %w", err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := lockComputerFile(ctx, lock); err != nil {
		return fmt.Errorf("lock bindings: %w", err)
	}
	defer unlockComputerFile(lock)
	return mutate()
}

func (s *BindingsStore) addOrRepair(all []WorkspaceBinding, binding WorkspaceBinding) error {
	binding = normalizeWorkspaceBinding(binding)
	for i := range all {
		if all[i].Environment == binding.Environment && all[i].WorkspaceID == binding.WorkspaceID {
			all[i] = binding
			return s.write(all)
		}
	}
	return s.write(append(all, binding))
}

func (s *BindingsStore) remove(all []WorkspaceBinding, environment, workspaceID string) error {
	environment = normalizeBindingEnvironment(environment)
	out := all[:0]
	for _, binding := range all {
		if binding.Environment != environment || binding.WorkspaceID != workspaceID {
			out = append(out, binding)
		}
	}
	return s.write(out)
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

func normalizeBindingEnvironment(environment string) string {
	environment = strings.ToLower(strings.TrimSpace(environment))
	if environment == "" {
		return string(cli.ServiceEnvironmentProduction)
	}
	return environment
}

func normalizeWorkspaceBinding(binding WorkspaceBinding) WorkspaceBinding {
	binding.Environment = normalizeBindingEnvironment(binding.Environment)
	if binding.Origin == "" && binding.Environment == string(cli.ServiceEnvironmentProduction) {
		binding.Origin = cli.OfficialCloudAPIURL
	}
	return binding
}
