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
	if !rec.ObserveReady(rec.StartIdentity()) || rec.Lifecycle != RunnerLifecycleRunning {
		t.Fatal("matching child Ready did not move lifecycle to running")
	}
}

func TestRunnerCrashRestartsAfterBackoffThenDegrades(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	rec := &RunnerRecord{Lifecycle: RunnerLifecycleStopped}
	rec.ObserveSpawn()
	rec.ObserveReady(rec.StartIdentity())
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
	rec.ObserveReady(rec.StartIdentity())
	rec.ObserveExit(now.Add(time.Second), RunnerExitCrash)
	rec.ObserveSpawn()
	rec.ObserveReady(rec.StartIdentity())
	rec.ObserveExit(now.Add(2*time.Second), RunnerExitCrash)
	if rec.Lifecycle != RunnerLifecycleDegraded {
		t.Fatalf("third crash in 60s lifecycle = %s, want degraded", rec.Lifecycle)
	}
	if rec.CanSpawn(true, now.Add(time.Hour)) {
		t.Fatal("degraded runner must not auto-spawn")
	}
}

func TestRunnerSpawnUsesFreshStartIdentityAcrossExit(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	rec := &RunnerRecord{Lifecycle: RunnerLifecycleStopped}
	if rec.StartIdentity() != "" {
		t.Fatalf("fresh start identity = %q", rec.StartIdentity())
	}
	rec.ObserveSpawn()
	rec.ObserveReady(rec.StartIdentity())
	first := rec.StartIdentity()
	if first == "" {
		t.Fatal("ObserveSpawn must allocate a start identity")
	}
	rec.ObserveExit(now, RunnerExitGraceful)
	if rec.StartIdentity() != first {
		t.Fatal("ObserveExit must not reset start identity")
	}
	rec.ObserveSpawn()
	rec.ObserveReady(rec.StartIdentity())
	if rec.StartIdentity() == first {
		t.Fatal("next spawn must not reuse the previous start identity")
	}
}

func TestRunnerUnlinkedAndGracefulExits(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	rec := &RunnerRecord{}
	rec.ObserveSpawn()
	rec.ObserveReady(rec.StartIdentity())
	rec.ObserveExit(now, RunnerExitUnlinked)
	if rec.Lifecycle != RunnerLifecycleDegraded || rec.CanSpawn(true, now) {
		t.Fatalf("unlinked runner = %s spawn=%v", rec.Lifecycle, rec.CanSpawn(true, now))
	}

	rec = &RunnerRecord{}
	rec.ObserveSpawn()
	rec.ObserveReady(rec.StartIdentity())
	rec.ObserveExit(now, RunnerExitGraceful)
	if rec.Lifecycle != RunnerLifecycleStopped || !rec.CanSpawn(true, now) {
		t.Fatalf("graceful stop = %s spawn=%v", rec.Lifecycle, rec.CanSpawn(true, now))
	}
}

func TestRunnerRecordAdoptsExternalPIDAndDoesNotSpawn(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	rec := &RunnerRecord{Lifecycle: RunnerLifecycleStopped}
	rec.AdoptExternalPID(4242)
	if rec.Lifecycle != RunnerLifecycleRunning || rec.HasChild() || rec.CanSpawn(true, now) {
		t.Fatalf("adopted runner = %+v spawn=%v", rec, rec.CanSpawn(true, now))
	}
	if !rec.ClearExternalPIDIfDead(false) || rec.ExternalPID != 0 || rec.Lifecycle != RunnerLifecycleStopped {
		t.Fatalf("dead adopted runner = %+v", rec)
	}
	if !rec.CanSpawn(true, now) {
		t.Fatal("dead adopted runner must become spawnable")
	}
}
