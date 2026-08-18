package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

const agentCredentialRefreshBeforeExpiry = time.Hour

type cachedAgentCredential struct {
	SchemaVersion int    `json:"schemaVersion"`
	CredentialID  string `json:"credentialId"`
	Token         string `json:"token"`
	Prefix        string `json:"tokenPrefix"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
	IssuedAt      string `json:"issuedAt"`
	ServerURL     string `json:"serverUrl"`
	WorkspaceID   string `json:"workspaceId"`
	RuntimeID     string `json:"runtimeId"`
	AgentID       string `json:"agentId"`
}

func agentCredentialCachePath(cfg Config, workspaceID, agentID string) string {
	return filepath.Join(workspaceStateRoot(cfg.WorkspacesRoot, workspaceID), "profiles", agentID, "credential.json")
}

func readCachedAgentCredential(cfg Config, workspaceID, runtimeID, agentID string, now time.Time) (cachedAgentCredential, bool) {
	return readCachedAgentCredentialFor(cfg, workspaceID, runtimeID, agentID, now, true)
}

// readCachedAgentCredentialForMessage resolves the durable credential owned by
// a local workspace-agent root. Message transport has no task, lease, or
// current-turn identity; runtime binding is checked when issued.
func readCachedAgentCredentialForMessage(cfg Config, workspaceID, agentID string, now time.Time) (cachedAgentCredential, bool) {
	return readCachedAgentCredentialFor(cfg, workspaceID, "", agentID, now, false)
}

func readCachedAgentCredentialFor(cfg Config, workspaceID, runtimeID, agentID string, now time.Time, requireRuntime bool) (cachedAgentCredential, bool) {
	if validateWorkspaceStatePath(cfg.WorkspacesRoot, workspaceID) != nil || validateStatePathPart("agent", agentID) != nil {
		return cachedAgentCredential{}, false
	}
	path := agentCredentialCachePath(cfg, workspaceID, agentID)
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return cachedAgentCredential{}, false
		}
		return readAndMigrateLegacyAgentCredential(cfg, workspaceID, runtimeID, agentID, now, requireRuntime)
	}
	var cached cachedAgentCredential
	if err := json.Unmarshal(raw, &cached); err != nil {
		return cachedAgentCredential{}, false
	}
	if !cached.validFor(cfg, workspaceID, runtimeID, agentID, now, requireRuntime) {
		return cachedAgentCredential{}, false
	}
	return cached, true
}

// TODO: Remove this legacy reader after one release cycle in which all agents
// have had a chance to migrate from runtime/credentials/current.json.
type legacyCachedAgentCredential struct {
	CredentialID string `json:"credential_id"`
	Token        string `json:"token"`
	Prefix       string `json:"token_prefix"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	IssuedAt     string `json:"issued_at"`
	ServerURL    string `json:"server_url"`
	WorkspaceID  string `json:"workspace_id"`
	RuntimeID    string `json:"runtime_id"`
	AgentID      string `json:"agent_id"`
}

func readAndMigrateLegacyAgentCredential(cfg Config, workspaceID, runtimeID, agentID string, now time.Time, requireRuntime bool) (cachedAgentCredential, bool) {
	legacyPath := filepath.Join(agentworkspace.Root(cfg.WorkspacesRoot, workspaceID, agentID), "runtime", "credentials", "current.json")
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		return cachedAgentCredential{}, false
	}
	var legacy legacyCachedAgentCredential
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return cachedAgentCredential{}, false
	}
	cached := cachedAgentCredential{
		SchemaVersion: 1,
		CredentialID:  legacy.CredentialID,
		Token:         legacy.Token,
		Prefix:        legacy.Prefix,
		ExpiresAt:     legacy.ExpiresAt,
		IssuedAt:      legacy.IssuedAt,
		ServerURL:     legacy.ServerURL,
		WorkspaceID:   legacy.WorkspaceID,
		RuntimeID:     legacy.RuntimeID,
		AgentID:       legacy.AgentID,
	}
	if !cached.validFor(cfg, workspaceID, runtimeID, agentID, now, requireRuntime) {
		return cachedAgentCredential{}, false
	}
	if _, err := writeCachedAgentCredential(cfg, workspaceID, cached.RuntimeID, agentID, AgentCredentialResponse{
		ID:        cached.CredentialID,
		AgentID:   cached.AgentID,
		Prefix:    cached.Prefix,
		ExpiresAt: stringPointer(cached.ExpiresAt),
		Token:     cached.Token,
	}, now); err != nil {
		return cachedAgentCredential{}, false
	}
	return cached, true
}

func stringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func writeCachedAgentCredential(cfg Config, workspaceID, runtimeID, agentID string, resp AgentCredentialResponse, now time.Time) (cachedAgentCredential, error) {
	if err := validateStatePathPart("workspace", workspaceID); err != nil {
		return cachedAgentCredential{}, err
	}
	if err := validateWorkspaceStatePath(cfg.WorkspacesRoot, workspaceID); err != nil {
		return cachedAgentCredential{}, err
	}
	if err := validateStatePathPart("agent", agentID); err != nil {
		return cachedAgentCredential{}, err
	}
	if strings.TrimSpace(resp.Token) == "" {
		return cachedAgentCredential{}, fmt.Errorf("ensure response missing token")
	}
	cached := cachedAgentCredential{
		SchemaVersion: 1,
		CredentialID:  resp.ID,
		Token:         resp.Token,
		Prefix:        resp.Prefix,
		IssuedAt:      now.UTC().Format(time.RFC3339Nano),
		ServerURL:     cfg.ServerBaseURL,
		WorkspaceID:   workspaceID,
		RuntimeID:     runtimeID,
		AgentID:       agentID,
	}
	if resp.ExpiresAt != nil {
		cached.ExpiresAt = *resp.ExpiresAt
	}
	path := agentCredentialCachePath(cfg, workspaceID, agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return cachedAgentCredential{}, fmt.Errorf("create agent credential cache dir: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return cachedAgentCredential{}, fmt.Errorf("chmod agent credential cache dir: %w", err)
	}
	raw, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		return cachedAgentCredential{}, fmt.Errorf("marshal agent credential cache: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".current-*.tmp")
	if err != nil {
		return cachedAgentCredential{}, fmt.Errorf("create agent credential cache temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return cachedAgentCredential{}, fmt.Errorf("chmod agent credential cache temp: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return cachedAgentCredential{}, fmt.Errorf("write agent credential cache temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return cachedAgentCredential{}, fmt.Errorf("close agent credential cache temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return cachedAgentCredential{}, fmt.Errorf("rename agent credential cache: %w", err)
	}
	cleanup = false
	if err := os.Chmod(path, 0o600); err != nil {
		return cachedAgentCredential{}, fmt.Errorf("chmod agent credential cache: %w", err)
	}
	return cached, nil
}

// removeCachedAgentCredential deletes the on-disk cached credential for
// (workspaceID, agentID). Called when the server reports this agent no
// longer belongs to this daemon's runtime (isAgentNotBoundToRuntimeError) —
// the cached token is for a binding that no longer exists, and must not be
// reused if this agent is later reassigned back to this runtime. Missing
// file is not an error (nothing to remove).
func removeCachedAgentCredential(cfg Config, workspaceID, agentID string) error {
	if err := validateStatePathPart("workspace", workspaceID); err != nil {
		return err
	}
	if err := validateWorkspaceStatePath(cfg.WorkspacesRoot, workspaceID); err != nil {
		return err
	}
	if err := validateStatePathPart("agent", agentID); err != nil {
		return err
	}
	path := agentCredentialCachePath(cfg, workspaceID, agentID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove agent credential cache: %w", err)
	}
	return nil
}

func (c cachedAgentCredential) validFor(cfg Config, workspaceID, runtimeID, agentID string, now time.Time, requireRuntime bool) bool {
	if strings.TrimSpace(c.Token) == "" {
		return false
	}
	if c.ServerURL != cfg.ServerBaseURL || c.WorkspaceID != workspaceID || c.AgentID != agentID {
		return false
	}
	if requireRuntime && c.RuntimeID != runtimeID {
		return false
	}
	if strings.TrimSpace(c.ExpiresAt) == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	if err != nil {
		return false
	}
	return expiresAt.After(now.Add(agentCredentialRefreshBeforeExpiry))
}
