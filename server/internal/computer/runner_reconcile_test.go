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
	if rec.CanSpawn(true, now) {
		t.Fatal("running child must not spawn again")
	}
}

func TestRunnerCrashRestartsAfterBackoffThenDegrades(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	rec := &RunnerRecord{Lifecycle: RunnerLifecycleStopped}
	rec.ObserveSpawn()
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
	rec.ObserveExit(now.Add(time.Second), RunnerExitCrash)
	rec.ObserveSpawn()
	rec.ObserveExit(now.Add(2*time.Second), RunnerExitCrash)
	if rec.Lifecycle != RunnerLifecycleDegraded {
		t.Fatalf("third crash in 60s lifecycle = %s, want degraded", rec.Lifecycle)
	}
	if rec.CanSpawn(true, now.Add(time.Hour)) {
		t.Fatal("degraded runner must not auto-spawn")
	}
}

func TestRunnerSpawnGenerationStaysMonotonicAcrossExit(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	rec := &RunnerRecord{Lifecycle: RunnerLifecycleStopped}
	if rec.Generation() != 0 {
		t.Fatalf("fresh generation = %d", rec.Generation())
	}
	rec.ObserveSpawn()
	first := rec.Generation()
	if first == 0 {
		t.Fatal("ObserveSpawn must allocate a generation")
	}
	rec.ObserveExit(now, RunnerExitGraceful)
	if rec.Generation() != first {
		t.Fatal("ObserveExit must not reset spawn generation")
	}
	rec.ObserveSpawn()
	if rec.Generation() <= first {
		t.Fatal("next spawn must not reuse the previous generation")
	}
}

func TestRunnerUnlinkedAndGracefulExits(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	rec := &RunnerRecord{}
	rec.ObserveSpawn()
	rec.ObserveExit(now, RunnerExitUnlinked)
	if rec.Lifecycle != RunnerLifecycleDegraded || rec.CanSpawn(true, now) {
		t.Fatalf("unlinked runner = %s spawn=%v", rec.Lifecycle, rec.CanSpawn(true, now))
	}

	rec = &RunnerRecord{}
	rec.ObserveSpawn()
	rec.ObserveExit(now, RunnerExitGraceful)
	if rec.Lifecycle != RunnerLifecycleStopped || !rec.CanSpawn(true, now) {
		t.Fatalf("graceful stop = %s spawn=%v", rec.Lifecycle, rec.CanSpawn(true, now))
	}
}
