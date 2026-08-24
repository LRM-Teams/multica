package daemon

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
)

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
