package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

func TestAgentCredentialCachePersistsMetadataWithoutPlaintextToken(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	cfg := Config{WorkspacesRoot: root, ServerBaseURL: "https://api.example.test"}
	cached, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-1", AgentID: "agent-1", Prefix: "sk_agent_123456", Token: "sk_agent_secret_token",
	}, now)
	if err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}
	if cached.Token != "sk_agent_secret_token" {
		t.Fatalf("cached token mismatch")
	}

	path := agentCredentialCachePath(cfg, "workspace-1", "agent-1")
	wantPath := filepath.Join(workspaceStateRoot(root, "workspace-1"), "profiles", "agent-1", "credential.json")
	if path != wantPath {
		t.Fatalf("cache path = %q, want %q", path, wantPath)
	}
	if runtime.GOOS != "windows" {
		if mode := mustStatMode(t, filepath.Dir(path)).Perm(); mode != 0o700 {
			t.Fatalf("cache dir mode = %o, want 0700", mode)
		}
		if mode := mustStatMode(t, path).Perm(); mode != 0o600 {
			t.Fatalf("cache file mode = %o, want 0600", mode)
		}
	}

	read, ok := readCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", now)
	if !ok {
		t.Fatal("expected cached credential to be reusable")
	}
	if read.Token != "" || read.CredentialID != "credential-1" {
		t.Fatalf("read cached credential = %#v", read)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk_agent_secret_token") || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("credential cache contains plaintext server credential: %s", raw)
	}
}

func TestAgentCredentialCacheInvalidatesScopeAndExpiredCredential(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	cfg := Config{WorkspacesRoot: root, ServerBaseURL: "https://api.example.test"}
	expiresAt := now.Add(-time.Minute).Format(time.RFC3339)
	_, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID:        "credential-1",
		AgentID:   "agent-1",
		Prefix:    "sk_agent_123456",
		ExpiresAt: &expiresAt,
		Token:     "sk_agent_secret_token",
	}, now)
	if err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}
	if _, ok := readCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", now); ok {
		t.Fatal("legacy expired credential should be rejected")
	}

	_, err = writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-2", AgentID: "agent-1", Prefix: "sk_agent_123456", Token: "launch-scoped-token",
	}, now)
	if err != nil {
		t.Fatalf("write non-expiring credential: %v", err)
	}
	if _, ok := readCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", now.Add(25*time.Hour)); !ok {
		t.Fatal("launch-scoped credential should remain valid after 24 hours")
	}

	_, err = writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-3", AgentID: "agent-1", Prefix: "sk_agent_fedcba", Token: "sk_agent_secret_token_3",
	}, now)
	if err != nil {
		t.Fatalf("writeCachedAgentCredential valid: %v", err)
	}
	for _, tc := range []struct {
		workspaceID string
		runtimeID   string
		agentID     string
	}{
		{workspaceID: "workspace-2", runtimeID: "runtime-1", agentID: "agent-1"},
		{workspaceID: "workspace-1", runtimeID: "runtime-2", agentID: "agent-1"},
		{workspaceID: "workspace-1", runtimeID: "runtime-1", agentID: "agent-2"},
	} {
		if _, ok := readCachedAgentCredential(cfg, tc.workspaceID, tc.runtimeID, tc.agentID, now); ok {
			t.Fatalf("scope mismatch should invalidate cache for %#v", tc)
		}
	}
}

func TestAgentCredentialCacheRejectsEmptyToken(t *testing.T) {
	_, err := writeCachedAgentCredential(Config{WorkspacesRoot: t.TempDir()}, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{}, time.Now())
	if err == nil {
		t.Fatal("expected empty ensure response token to fail")
	}
}

func TestAgentCredentialCacheMigratesLegacyWorkspaceCredential(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	cfg := Config{WorkspacesRoot: root, ServerBaseURL: "https://api.example.test"}
	expiresAt := now.Add(30 * 24 * time.Hour).Format(time.RFC3339)
	legacyPath := filepath.Join(agentworkspace.Root(root, "workspace-1", "agent-1"), "runtime", "credentials", "current.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("create legacy credential dir: %v", err)
	}
	legacy := map[string]string{
		"credential_id": "credential-legacy",
		"token":         "mac_legacy_token",
		"token_prefix":  "mac_legacy",
		"expires_at":    expiresAt,
		"issued_at":     now.Format(time.RFC3339Nano),
		"server_url":    cfg.ServerBaseURL,
		"workspace_id":  "workspace-1",
		"runtime_id":    "runtime-1",
		"agent_id":      "agent-1",
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy credential: %v", err)
	}
	if err := os.WriteFile(legacyPath, raw, 0o600); err != nil {
		t.Fatalf("write legacy credential: %v", err)
	}

	cached, ok := readCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", now)
	if !ok || cached.Token != "" {
		t.Fatalf("read legacy credential = %#v, ok=%t", cached, ok)
	}
	if strict, ok := readCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", now); !ok || strict.Token != "" {
		t.Fatalf("migrated credential failed strict runtime read: %#v, ok=%t", strict, ok)
	}
	newPath := agentCredentialCachePath(cfg, "workspace-1", "agent-1")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("migrated credential missing: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy plaintext credential survived migration: %v", err)
	}
}

func TestAgentCredentialCacheRetiresLegacyPlaintextBesideModernMetadata(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	cfg := Config{WorkspacesRoot: root, ServerBaseURL: "https://api.example.test"}
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-1", AgentID: "agent-1", Token: "sk_agent_secret",
	}, now); err != nil {
		t.Fatal(err)
	}
	legacyPath := legacyAgentCredentialCachePath(cfg, "workspace-1", "agent-1")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"token":"exposed-legacy-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", now); !ok {
		t.Fatal("modern credential metadata should remain valid")
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy plaintext credential survived modern metadata read: %v", err)
	}
}

func TestAgentCredentialCacheDeletesMalformedLegacyPlaintext(t *testing.T) {
	root := t.TempDir()
	cfg := Config{WorkspacesRoot: root, ServerBaseURL: "https://api.example.test"}
	legacyPath := legacyAgentCredentialCachePath(cfg, "workspace-1", "agent-1")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"token":"exposed"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", time.Now()); ok {
		t.Fatal("malformed legacy credential unexpectedly authenticated")
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("malformed legacy plaintext credential survived read: %v", err)
	}
}

func TestAgentCredentialCacheRunCopyCleanup(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "multica-real")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	wrapperDir, _, err := prepareTaskCLITransport(Config{WorkspacesRoot: root}, "workspace-1", "agent-1", "run-1", bin, "durable-token-copy")
	if err != nil {
		t.Fatalf("prepareTaskCLITransport: %v", err)
	}
	if err := os.RemoveAll(wrapperDir); err != nil {
		t.Fatalf("cleanup wrapper dir: %v", err)
	}
	if _, err := os.Stat(wrapperDir); !os.IsNotExist(err) {
		t.Fatalf("wrapper dir should be removed after durable run cleanup, stat err=%v", err)
	}
}
