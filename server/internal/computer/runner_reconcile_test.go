package computer

import (
	"testing"
	"time"
)

func TestRunnerRecordCanSpawnOnlyWhenWantedAndIdle(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	if !(*RunnerRecord)(nil).CanSpawn(true, now) {
		t.Fatal("missing record must spawn a wanted Binding")
	}
	rec := &RunnerRecord{Lifecycle: RunnerLifecycleStopped}
	if rec.CanSpawn(false, now) {
		t.Fatal("unwanted Binding must not spawn")
	}
	rec.ObserveSpawn()
	if rec.Lifecycle != RunnerLifecycleStarting {
		t.Fatalf("spawn lifecycle = %s, want starting", rec.Lifecycle)
	}
	if rec.CanSpawn(true, now) {
		t.Fatal("running child must not spawn again")
	}
	if !rec.ObserveReady("child-a") || rec.Lifecycle != RunnerLifecycleRunning {
		t.Fatal("matching child Ready did not move lifecycle to running")
	}
}

func TestRunnerCrashRestartsAfterBackoffThenDegrades(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	rec := &RunnerRecord{Lifecycle: RunnerLifecycleStopped}
	rec.ObserveSpawn()
	rec.ObserveReady("child-a")
	rec.ObserveExit(now, RunnerExitCrash)
	if rec.Lifecycle != RunnerLifecycleCrashed {
		t.Fatalf("first crash lifecycle = %s, want crashed", rec.Lifecycle)
	}
	if rec.CanSpawn(true, now) {
		t.Fatal("crash must wait for backoff")
	}
	if !rec.CanSpawn(true, now.Add(RunnerRestartBackoff)) {
		t.Fatal("crash must be spawnable after 2s backoff")
	}

	rec.ObserveSpawn()
	rec.ObserveReady("child-b")
	rec.ObserveExit(now.Add(time.Second), RunnerExitCrash)
	rec.ObserveSpawn()
	rec.ObserveReady("child-c")
	rec.ObserveExit(now.Add(2*time.Second), RunnerExitCrash)
	if rec.Lifecycle != RunnerLifecycleDegraded {
		t.Fatalf("third crash in 60s lifecycle = %s, want degraded", rec.Lifecycle)
	}
	if rec.CanSpawn(true, now.Add(time.Hour)) {
		t.Fatal("degraded runner must not auto-spawn")
	}
}

func TestRunnerSpawnRecordsChildDaemonInstanceOnReady(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	rec := &RunnerRecord{Lifecycle: RunnerLifecycleStopped}
	if rec.DaemonInstanceID() != "" {
		t.Fatalf("fresh daemon instance = %q", rec.DaemonInstanceID())
	}
	rec.ObserveSpawn()
	if rec.DaemonInstanceID() != "" {
		t.Fatal("ObserveSpawn must not mint a ComputerCore daemon instance")
	}
	if rec.ObserveReady("") {
		t.Fatal("empty child Ready must not become running")
	}
	if !rec.ObserveReady("child-a") || rec.DaemonInstanceID() != "child-a" {
		t.Fatal("ObserveReady must record the child-reported daemon instance")
	}
	rec.ObserveExit(now, RunnerExitGraceful)
	rec.ObserveSpawn()
	if rec.DaemonInstanceID() != "" {
		t.Fatal("next spawn must clear the previous child identity until Ready")
	}
	if !rec.ObserveReady("child-b") || rec.DaemonInstanceID() == "child-a" {
		t.Fatal("next Ready must record a new child-reported daemon instance")
	}
}

func TestRunnerUnlinkedAndGracefulExits(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	rec := &RunnerRecord{}
	rec.ObserveSpawn()
	rec.ObserveReady("child-a")
	rec.ObserveExit(now, RunnerExitUnlinked)
	if rec.Lifecycle != RunnerLifecycleDegraded || rec.CanSpawn(true, now) {
		t.Fatalf("unlinked runner = %s spawn=%v", rec.Lifecycle, rec.CanSpawn(true, now))
	}

	rec = &RunnerRecord{}
	rec.ObserveSpawn()
	rec.ObserveReady("child-a")
	rec.ObserveExit(now, RunnerExitGraceful)
	if rec.Lifecycle != RunnerLifecycleStopped || !rec.CanSpawn(true, now) {
		t.Fatalf("graceful stop = %s spawn=%v", rec.Lifecycle, rec.CanSpawn(true, now))
	}
}
