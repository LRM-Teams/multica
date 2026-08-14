package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
)

func newComputerSyncTestDaemon(t *testing.T, workspaceResponse string, agents map[string]AgentEntry) (*Daemon, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/daemon/computer/heartbeat" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true,"generation":1}`)
			return
		}
		if r.URL.Path != "/api/workspaces" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, workspaceResponse)
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	d := New(Config{
		ServerBaseURL:      server.URL,
		DaemonID:           "computer-1",
		BindingsRoot:       root,
		ComputerGeneration: 1,
		Agents:             agents,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetToken("machine-session")
	return d, root
}

func TestComputerSyncMembershipLossStopsLastWorkspaceBeforeReturningError(t *testing.T) {
	d, root := newComputerSyncTestDaemon(t, `[]`, map[string]AgentEntry{"codex": {}})
	if err := computer.NewBindingsStore(root).AddOrRepair(computer.WorkspaceBinding{
		WorkspaceID: "workspace-1", ComputerID: "computer-1", Credential: "secret",
		CredentialExpiresAt: time.Now().Add(time.Hour), Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	d.workspaces["workspace-1"] = newWorkspaceState("workspace-1", []string{"runtime-1"})
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1"}
	d.client.SetWorkspaceDaemonToken("workspace-1", "secret", time.Now().Add(time.Hour))

	err := d.syncWorkspacesFromAPI(context.Background())
	if err == nil {
		t.Fatal("membership loss should report that no configured connection remains")
	}
	if _, ok := d.workspaces["workspace-1"]; ok {
		t.Fatal("revoked last Workspace was left running after sync error")
	}
	if _, ok := d.runtimeIndex["runtime-1"]; ok {
		t.Fatal("revoked Workspace runtime remained executable")
	}
	if !d.serverConnected.Load() {
		t.Fatal("successful authenticated membership round-trip should keep Computer connected truth")
	}
	if _, ok, err := computer.NewBindingsStore(root).Get("workspace-1"); err != nil || !ok {
		t.Fatalf("local connection evidence must be retained for reversible membership recovery: ok=%v err=%v", ok, err)
	}
}

func TestComputerSyncZeroAgentWorkspaceIsConnectedWithoutRuntime(t *testing.T) {
	d, root := newComputerSyncTestDaemon(t, `[{"id":"workspace-1","name":"Team"}]`, map[string]AgentEntry{})
	if err := computer.NewBindingsStore(root).AddOrRepair(computer.WorkspaceBinding{
		WorkspaceID: "workspace-1", ComputerID: "computer-1", Credential: "secret",
		CredentialExpiresAt: time.Now().Add(time.Hour), Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.syncWorkspacesFromAPI(context.Background()); err != nil {
		t.Fatalf("zero-Agent Workspace connection should be valid: %v", err)
	}
	if _, ok := d.workspaces["workspace-1"]; !ok {
		t.Fatal("zero-Agent Workspace was not tracked")
	}
	if len(d.allRuntimeIDs()) != 0 {
		t.Fatalf("zero-Agent Workspace unexpectedly created runtimes: %v", d.allRuntimeIDs())
	}
	if !d.serverConnected.Load() {
		t.Fatal("authenticated zero-Agent Computer should report connected")
	}
}

func TestComputerSyncSelectedWorkspaceFailureDoesNotBlockHealthySibling(t *testing.T) {
	d, root := newComputerSyncTestDaemon(t, `[{"id":"workspace-healthy","name":"Healthy"}]`, map[string]AgentEntry{})
	d.cfg.WorkspaceID = "workspace-unavailable"
	for _, id := range []string{"workspace-unavailable", "workspace-healthy"} {
		if err := computer.NewBindingsStore(root).AddOrRepair(computer.WorkspaceBinding{
			WorkspaceID: id, ComputerID: "computer-1", Credential: "secret-" + id,
			CredentialExpiresAt: time.Now().Add(time.Hour), Active: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	d.workspaces["workspace-unavailable"] = newWorkspaceState("workspace-unavailable", nil)

	if err := d.syncWorkspacesFromAPI(context.Background()); err != nil {
		t.Fatalf("one unavailable selected Workspace must not terminate healthy siblings: %v", err)
	}
	if _, ok := d.workspaces["workspace-healthy"]; !ok {
		t.Fatal("healthy sibling Workspace was not restored")
	}
	if _, ok := d.workspaces["workspace-unavailable"]; ok {
		t.Fatal("unavailable selected Workspace was left active")
	}
}

func TestConfiguredWorkspaceBindingsUseOnlyCurrentEnvironment(t *testing.T) {
	root := t.TempDir()
	store := computer.NewBindingsStore(root)
	for _, binding := range []computer.WorkspaceBinding{
		{Environment: "production", Origin: "https://leagent.me", WorkspaceID: "same-id", ComputerID: "computer-1", Credential: "prod", Active: true},
		{Environment: "test", Origin: "https://test.leagent.me", WorkspaceID: "same-id", ComputerID: "computer-1", Credential: "test", Active: true},
	} {
		if err := store.AddOrRepair(binding); err != nil {
			t.Fatal(err)
		}
	}
	d := &Daemon{cfg: Config{Environment: "test", BindingsRoot: root, DaemonID: "computer-1"}}
	got, err := d.configuredWorkspaceBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["same-id"].Credential != "test" {
		t.Fatalf("current-environment connections = %+v", got)
	}
}

func TestRegisterTokenRefreshPersistsBindingCredentialForRestart(t *testing.T) {
	root := t.TempDir()
	store := computer.NewBindingsStore(root)
	originalAcceptedAt := time.Now().Add(-24 * time.Hour).UTC()
	if err := store.AddOrRepair(computer.WorkspaceBinding{
		Environment: "test", Origin: "https://test.example.com",
		WorkspaceID: "workspace-1", WorkspaceSlug: "team", ComputerID: "computer-1",
		Credential: "expired-token", CredentialExpiresAt: time.Now().Add(-time.Hour),
		AcceptedAt: originalAcceptedAt, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	d := New(Config{
		Environment: "test", BindingsRoot: root, DaemonID: "computer-1",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Microsecond)
	applied, err := d.applyRegisterDaemonToken("workspace-1", &RegisterResponse{
		DaemonToken: "rotated-token", DaemonTokenExpiresAt: expiresAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("apply register token: %v", err)
	}
	if !applied {
		t.Fatal("register token was not accepted")
	}

	reloaded, ok, err := computer.NewBindingsStore(root).GetForEnvironment("test", "workspace-1")
	if err != nil || !ok {
		t.Fatalf("reload refreshed Binding: ok=%v err=%v", ok, err)
	}
	if reloaded.Credential != "rotated-token" || !reloaded.CredentialExpiresAt.Equal(expiresAt) {
		t.Fatalf("persisted credential = %q until %s", reloaded.Credential, reloaded.CredentialExpiresAt)
	}
	if reloaded.WorkspaceSlug != "team" || reloaded.Origin != "https://test.example.com" || !reloaded.AcceptedAt.Equal(originalAcceptedAt) || !reloaded.Active {
		t.Fatalf("credential refresh mutated Binding identity/metadata: %+v", reloaded)
	}
}
