package computer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestDiagnoseReadOnlyAndReflectsResident(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any {
		return map[string]any{"status": "running", "connected": true, "daemon_id": "d1"}
	}

	d := lc.Diagnose()
	if d.Resident != "running" || !d.Connected {
		t.Fatalf("doctor = %+v, want running+connected from resident (not agent)", d)
	}
	if d.IdentityState == "" {
		t.Fatalf("doctor missing identity_state")
	}
}

func TestDiagnoseRunningButServerDisconnected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any {
		return map[string]any{"status": "running", "connected": false, "agents": []any{"fresh-agent"}}
	}

	d := lc.Diagnose()
	if d.Resident != "running" || d.Connected {
		t.Fatalf("doctor = %+v, local process or Agent health must not imply server connectivity", d)
	}
}

func TestDiagnoseStoppedIsDisconnected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any { return map[string]any{"status": "stopped"} }

	d := lc.Diagnose()
	if d.Resident != "stopped" || d.Connected {
		t.Fatalf("doctor = %+v, want stopped+disconnected", d)
	}
	// doctor never goes through the fix path, and never writes anything.
	if len(d.FixApplied) != 0 {
		t.Fatalf("read-only diagnose must report no mutations: %+v", d)
	}
}

func TestDiagnoseReportsConfigResidentDriftAndPreservedMigrationEvidence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := cli.SaveCLIConfig(cli.CLIConfig{
		Environment: "test",
		ServerURL:   "https://test.leagent.me",
		AppURL:      "https://test.leagent.me",
	}); err != nil {
		t.Fatal(err)
	}
	legacyDir := filepath.Join(home, ".multica", "profiles", "old")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyID := "e0d441a0-897c-40be-b303-f2fc2877bd2f"
	if err := os.WriteFile(filepath.Join(legacyDir, "daemon.id"), []byte(legacyID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	lc := &Lifecycle{}
	lc.Probe = func(context.Context, string) map[string]any {
		return map[string]any{
			"status":          "running",
			"connected":       true,
			"environment":     "production",
			"server_url":      "https://api.leagent.me",
			"release_channel": "latest",
		}
	}
	d := lc.Diagnose()
	if !d.ConfigurationDrift || d.ResidentEnvironment != "production" || d.ResidentPackageSource != "stable" {
		t.Fatalf("doctor did not report configured/resident drift: %+v", d)
	}
	if d.IdentityState != "ambiguous" || len(d.LegacyIdentityCandidates) != 1 || d.LegacyIdentityCandidates[0] != legacyID {
		t.Fatalf("doctor did not report preserved migration evidence: %+v", d)
	}
}
