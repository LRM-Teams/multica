package daemon

import "testing"

func TestMemoryCurationRunGuardSerializesPerRuntime(t *testing.T) {
	d := &WorkspaceDaemonCore{activeCurationRuns: map[string]string{}}
	if !d.beginMemoryCurationRun("runtime-1", "run-1") {
		t.Fatal("first run was not accepted")
	}
	if d.beginMemoryCurationRun("runtime-1", "run-2") {
		t.Fatal("second run on the same runtime was accepted concurrently")
	}
	if got := d.activeMemoryCurationRun("runtime-1"); got != "run-1" {
		t.Fatalf("active run = %q, want run-1", got)
	}
	d.finishMemoryCurationRun("runtime-1", "run-other")
	if got := d.activeMemoryCurationRun("runtime-1"); got != "run-1" {
		t.Fatalf("mismatched completion cleared active run: %q", got)
	}
	d.finishMemoryCurationRun("runtime-1", "run-1")
	if got := d.activeMemoryCurationRun("runtime-1"); got != "" {
		t.Fatalf("active run after completion = %q, want empty", got)
	}
}
