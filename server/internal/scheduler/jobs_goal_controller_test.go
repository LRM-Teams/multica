package scheduler

import (
	"testing"
	"time"
)

func TestGoalControllerJobContract(t *testing.T) {
	job := GoalControllerJob(nil)
	if job.Name != JobNameGoalController {
		t.Fatalf("name = %q", job.Name)
	}
	if job.Cadence != 5*time.Second || job.CatchUpMode != CatchUpLatestOnly {
		t.Fatalf("unexpected schedule: cadence=%s catch_up=%s", job.Cadence, job.CatchUpMode)
	}
	if job.Handler == nil || job.Scopes == nil {
		t.Fatal("goal controller job must have a handler and scope")
	}
}
