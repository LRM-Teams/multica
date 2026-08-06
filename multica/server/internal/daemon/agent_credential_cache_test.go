package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentCredentialCacheWrites0600AndReusesValidCredential(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	cfg := Config{WorkspacesRoot: root, ServerBaseURL: "https://api.example.test"}
	expiresAt := now.Add(30 * 24 * time.Hour).Format(time.RFC3339)

	cached, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID:        "credential-1",
		AgentID:   "agent-1",
		Prefix:    "mac_123456",
		ExpiresAt: &expiresAt,
		Token:     "mac_secret_token",
	}, now)
	if err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}
	if cached.Token != "mac_secret_token" {
		t.Fatalf("cached token mismatch")
	}

	path := agentCredentialCachePath(cfg, "workspace-1", "agent-1")
	wantPath := filepath.Join(root, "workspace-1", ".multica", "agents", "agent-1", "runtime", "credentials", "current.json")
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
	if read.Token != "mac_secret_token" || read.CredentialID != "credential-1" {
		t.Fatalf("read cached credential = %#v", read)
	}
}

func TestAgentCredentialCacheInvalidatesScopeAndNearExpiry(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	cfg := Config{WorkspacesRoot: root, ServerBaseURL: "https://api.example.test"}
	expiresAt := now.Add(agentCredentialRefreshBeforeExpiry / 2).Format(time.RFC3339)
	_, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID:        "credential-1",
		AgentID:   "agent-1",
		Prefix:    "mac_123456",
		ExpiresAt: &expiresAt,
		Token:     "mac_secret_token",
	}, now)
	if err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}
	if _, ok := readCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", now); ok {
		t.Fatal("near-expiry credential should force re-ensure")
	}

	_, err = writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID:      "credential-2",
		AgentID: "agent-1",
		Prefix:  "mac_abcdef",
		Token:   "mac_secret_token_2",
	}, now)
	if err != nil {
		t.Fatalf("writeCachedAgentCredential no-expiry: %v", err)
	}
	if _, ok := readCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", now); ok {
		t.Fatal("credential without expires_at should force re-ensure")
	}

	validExpiresAt := now.Add(30 * 24 * time.Hour).Format(time.RFC3339)
	_, err = writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID:        "credential-3",
		AgentID:   "agent-1",
		Prefix:    "mac_fedcba",
		ExpiresAt: &validExpiresAt,
		Token:     "mac_secret_token_3",
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

func TestEnsureTaskAgentCredentialMissingBindingFailsBeforeFallback(t *testing.T) {
	d := &Daemon{cfg: Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: "https://api.example.test"}}
	if _, err := d.ensureTaskAgentCredential(context.Background(), Task{WorkspaceID: "workspace-1", RuntimeID: "", AgentID: "agent-1"}, nil); err == nil {
		t.Fatal("expected missing runtime binding to fail")
	}
}

func TestEnsureTaskAgentCredentialValidatesCachedCredentialEveryRun(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour).Format(time.RFC3339)

	var calls atomic.Int32
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode ensure body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"credential-1","agent_id":"agent-1","token_prefix":"mat_cached","expires_at":"` + expiresAt + `","reused":true}`))
	}))
	defer srv.Close()

	cfg := Config{WorkspacesRoot: root, ServerBaseURL: srv.URL}
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID:        "credential-1",
		AgentID:   "agent-1",
		Prefix:    "mat_cached",
		ExpiresAt: &expiresAt,
		Token:     "mat_cached_secret",
	}, now); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	d := &Daemon{cfg: cfg, client: NewClient(srv.URL)}
	token, err := d.ensureTaskAgentCredential(
		context.Background(),
		Task{WorkspaceID: "workspace-1", RuntimeID: "runtime-1", AgentID: "agent-1"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("ensure cached credential: %v", err)
	}
	if token != "mat_cached_secret" {
		t.Fatalf("token = %q, want cached raw token", token)
	}
	if calls.Load() != 1 {
		t.Fatalf("ensure calls = %d, want 1 live validation per run", calls.Load())
	}
	if got, _ := body["credential_id"].(string); got != "credential-1" {
		t.Fatalf("credential_id = %q, want credential-1", got)
	}
}

// TestEnsureTaskAgentCredentialClearsCacheOnAgentReassigned reproduces the
// 2026-07-31 production incident: an agent reassigned to a different
// runtime (agent.runtime_id is user-editable — a normal operation), where
// this daemon's cached credential is now for a binding that no longer
// exists. ensureTaskAgentCredential must classify the resulting 403 as
// errAgentReassignedElsewhere (not a generic credential error) and remove
// the now-stale cache entry so a future reassignment back to this runtime
// cannot reuse it.
func TestEnsureTaskAgentCredentialClearsCacheOnAgentReassigned(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour).Format(time.RFC3339)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"agent is not bound to this runtime"}`))
	}))
	defer srv.Close()

	cfg := Config{WorkspacesRoot: root, ServerBaseURL: srv.URL}
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-old", "agent-1", AgentCredentialResponse{
		ID:        "credential-1",
		AgentID:   "agent-1",
		Prefix:    "mat_stale",
		ExpiresAt: &expiresAt,
		Token:     "mat_stale_secret",
	}, now); err != nil {
		t.Fatalf("write stale cache: %v", err)
	}
	cachePath := agentCredentialCachePath(cfg, "workspace-1", "agent-1")
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("precondition: stale cache file should exist: %v", err)
	}

	d := &Daemon{cfg: cfg, client: NewClient(srv.URL)}
	_, err := d.ensureTaskAgentCredential(
		context.Background(),
		Task{WorkspaceID: "workspace-1", RuntimeID: "runtime-old", AgentID: "agent-1"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if !errors.Is(err, errAgentReassignedElsewhere) {
		t.Fatalf("ensureTaskAgentCredential error = %v, want errAgentReassignedElsewhere", err)
	}
	if _, statErr := os.Stat(cachePath); !os.IsNotExist(statErr) {
		t.Fatalf("stale cache file should have been removed, stat err = %v", statErr)
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
