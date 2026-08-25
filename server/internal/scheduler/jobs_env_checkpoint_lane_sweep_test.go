package scheduler

import (
	"testing"
	"time"
)

func TestEnvCheckpointLaneSweepJobSpec(t *testing.T) {
	job := EnvCheckpointLaneSweepJob(nil)
	if job.Name != JobNameEnvCheckpointLaneSweep {
		t.Fatalf("Name = %q, want %q", job.Name, JobNameEnvCheckpointLaneSweep)
	}
	if err := job.validate(); err != nil {
		t.Fatalf("job did not validate: %v", err)
	}
	// The sweep is global: lanes are found across every workspace, so a
	// per-workspace scope would silently leave lanes in workspaces the
	// scheduler happened not to shard over.
	scopes, err := job.Scopes(t.Context(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 1 || scopes[0] != ScopeGlobal {
		t.Fatalf("Scopes = %v, want [%v]", scopes, ScopeGlobal)
	}
}

func TestEnvCheckpointLaneSweepSkipsWithoutDatabase(t *testing.T) {
	res, err := EnvCheckpointLaneSweepJob(nil).Handler(t.Context(), HandlerInput{PlanTime: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result["skipped"] != true || res.Result["reason"] != "database_unavailable" {
		t.Fatalf("handler result = %#v, want skipped/database_unavailable", res.Result)
	}
}

// TestEnvCheckpointLaneSweepStaleCutoffTrailsPlanTime pins the direction of the
// cutoff. Sweeping fails a lane and orphans whatever it already built, so the
// only safe error is to sweep too late: the cutoff must sit a full staleness
// window behind the plan, never ahead of it.
func TestEnvCheckpointLaneSweepStaleCutoffTrailsPlanTime(t *testing.T) {
	plan := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	cutoff := envCheckpointLaneStaleCutoff(plan)
	if !cutoff.Before(plan) {
		t.Fatalf("cutoff %s must precede plan %s", cutoff, plan)
	}
	if got := plan.Sub(cutoff); got != envCheckpointLaneStaleAfter {
		t.Fatalf("cutoff trails plan by %s, want %s", got, envCheckpointLaneStaleAfter)
	}
	// A lane materializing normally must not be swept out from under itself.
	if !envCheckpointLaneStaleCutoff(plan).Before(plan.Add(-time.Minute)) {
		t.Fatal("a lane one minute old must be well outside the sweep window")
	}
}
