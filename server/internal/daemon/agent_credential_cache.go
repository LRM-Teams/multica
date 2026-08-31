package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

type cachedAgentCredential struct {
	SchemaVersion int    `json:"schemaVersion"`
	CredentialID  string `json:"credentialId"`
	// Token is held only by the in-memory Agent Proxy registration. It must
	// never be serialized into the workspace tree, which is readable by the
	// resident Agent process running under the same OS account.
	Token       string `json:"-"`
	Prefix      string `json:"tokenPrefix"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
	IssuedAt    string `json:"issuedAt"`
	ServerURL   string `json:"serverUrl"`
	WorkspaceID string `json:"workspaceId"`
	RuntimeID   string `json:"runtimeId"`
	AgentID     string `json:"agentId"`
}

func agentCredentialCachePath(cfg Config, workspaceID, agentID string) string {
	return filepath.Join(workspaceStateRoot(cfg.WorkspacesRoot, workspaceID), "profiles", agentID, "credential.json")
}

func legacyAgentCredentialCachePath(cfg Config, workspaceID, agentID string) string {
	return filepath.Join(agentworkspace.Root(cfg.WorkspacesRoot, workspaceID, agentID), "runtime", "credentials", "current.json")
}

func readCachedAgentCredential(cfg Config, workspaceID, runtimeID, agentID string, now time.Time) (cachedAgentCredential, bool) {
	return readCachedAgentCredentialFor(cfg, workspaceID, runtimeID, agentID, now, true)
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
	// A prior migration may have written modern metadata but failed to remove
	// the legacy plaintext file. Retry retirement on every successful read.
	if err := os.Remove(legacyAgentCredentialCachePath(cfg, workspaceID, agentID)); err != nil && !os.IsNotExist(err) {
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
	legacyPath := legacyAgentCredentialCachePath(cfg, workspaceID, agentID)
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		if !os.IsNotExist(err) {
			_ = os.Remove(legacyPath)
		}
		return cachedAgentCredential{}, false
	}
	var legacy legacyCachedAgentCredential
	if err := json.Unmarshal(raw, &legacy); err != nil {
		_ = os.Remove(legacyPath)
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
		_ = os.Remove(legacyPath)
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
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return cachedAgentCredential{}, false
	}
	cached.Token = ""
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

func removeCachedAgentCredentialIfMatches(cfg Config, workspaceID, runtimeID, agentID, credentialID string) error {
	cached, ok := readCachedAgentCredential(cfg, workspaceID, runtimeID, agentID, time.Now())
	if !ok || cached.CredentialID != credentialID {
		return nil
	}
	return removeCachedAgentCredential(cfg, workspaceID, agentID)
}

func (d *Daemon) messageAgentCredential(_ context.Context, workspaceID, agentID string) (cachedAgentCredential, error) {
	runner := d.currentWorkspaceDaemon(workspaceID)
	if runner == nil {
		return cachedAgentCredential{}, errors.New("Agent message runtime is unavailable")
	}
	runtimeID := runner.messageRuntimeID(agentID)
	credential, ok := d.activeAgentProxyServerCredential(workspaceID, runtimeID, agentID)
	if !ok {
		return cachedAgentCredential{}, errors.New("Agent launch credential is unavailable")
	}
	return credential, nil
}

func (c cachedAgentCredential) validFor(cfg Config, workspaceID, runtimeID, agentID string, now time.Time, requireRuntime bool) bool {
	if strings.TrimSpace(c.CredentialID) == "" {
		return false
	}
	if c.ServerURL != cfg.ServerBaseURL || c.WorkspaceID != workspaceID || c.AgentID != agentID {
		return false
	}
	if requireRuntime && c.RuntimeID != runtimeID {
		return false
	}
	// An empty expiry is intentional for daemon-issued credentials: their
	// lifetime is the resident Agent launch and the server revokes them when
	// that launch ends.
	if strings.TrimSpace(c.ExpiresAt) == "" {
		return true
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	if err != nil {
		return false
	}
	return expiresAt.After(now)
}
