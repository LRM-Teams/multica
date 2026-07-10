package scheduler

import (
	"testing"
	"time"
)

func TestAgentRadarScheduleJobSpec(t *testing.T) {
	job := AgentRadarScheduleJob(nil)
	if job.Name != JobNameAgentRadarSchedule {
		t.Fatalf("Name = %q, want %q", job.Name, JobNameAgentRadarSchedule)
	}
	if job.Cadence != 10*time.Minute {
		t.Fatalf("Cadence = %s, want 10m", job.Cadence)
	}
	if job.CatchUpMode != CatchUpLatestOnly {
		t.Fatalf("CatchUpMode = %q, want latest_only", job.CatchUpMode)
	}
	if err := job.validate(); err != nil {
		t.Fatalf("job did not validate: %v", err)
	}
	res, err := job.Handler(t.Context(), HandlerInput{PlanTime: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result["reason"] != "db_unavailable" {
		t.Fatalf("nil-db handler result = %#v, want db_unavailable skip", res.Result)
	}
}
