package computer

import (
	"context"
	"testing"
)

func TestDiagnoseReadOnlyAndReflectsResident(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lc := &Lifecycle{}
	lc.Probe = func(context.Context, int) map[string]any {
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
	lc.Probe = func(context.Context, int) map[string]any {
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
	lc.Probe = func(context.Context, int) map[string]any { return map[string]any{"status": "stopped"} }

	d := lc.Diagnose()
	if d.Resident != "stopped" || d.Connected {
		t.Fatalf("doctor = %+v, want stopped+disconnected", d)
	}
	// doctor never goes through the fix path, and never writes anything.
	if len(d.FixApplied) != 0 {
		t.Fatalf("read-only diagnose must report no mutations: %+v", d)
	}
}
