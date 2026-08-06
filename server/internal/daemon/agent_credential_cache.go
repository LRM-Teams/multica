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

func agentCredentialCachePath(cfg Config, workspaceID, agentID string) string {
	return filepath.Join(agentworkspace.Root(cfg.WorkspacesRoot, workspaceID, agentID), "runtime", "credentials", "current.json")
}

func readCachedAgentCredential(cfg Config, workspaceID, runtimeID, agentID string, now time.Time) (cachedAgentCredential, bool) {
	return readCachedAgentCredentialFor(cfg, workspaceID, runtimeID, agentID, now, true)
}

// readCachedAgentCredentialForChat resolves the durable credential owned by a
// local workspace-agent root. Chat transport has no task, lease, or current
// turn identity; runtime binding is checked when the credential is issued.
func readCachedAgentCredentialForChat(cfg Config, workspaceID, agentID string, now time.Time) (cachedAgentCredential, bool) {
	return readCachedAgentCredentialFor(cfg, workspaceID, "", agentID, now, false)
}

func readCachedAgentCredentialFor(cfg Config, workspaceID, runtimeID, agentID string, now time.Time, requireRuntime bool) (cachedAgentCredential, bool) {
	path := agentCredentialCachePath(cfg, workspaceID, agentID)
	raw, err := os.ReadFile(path)
	if err != nil {
		return cachedAgentCredential{}, false
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

func writeCachedAgentCredential(cfg Config, workspaceID, runtimeID, agentID string, resp AgentCredentialResponse, now time.Time) (cachedAgentCredential, error) {
	if strings.TrimSpace(resp.Token) == "" {
		return cachedAgentCredential{}, fmt.Errorf("ensure response missing token")
	}
	cached := cachedAgentCredential{
		CredentialID: resp.ID,
		Token:        resp.Token,
		Prefix:       resp.Prefix,
		IssuedAt:     now.UTC().Format(time.RFC3339Nano),
		ServerURL:    cfg.ServerBaseURL,
		WorkspaceID:  workspaceID,
		RuntimeID:    runtimeID,
		AgentID:      agentID,
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
