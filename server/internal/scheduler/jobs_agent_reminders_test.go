package scheduler

import (
	"testing"
	"time"
)

func TestAgentReminderFireJobSpec(t *testing.T) {
	job := AgentReminderFireJob(nil, nil)
	if job.Name != JobNameAgentReminderFire {
		t.Fatalf("Name = %q", job.Name)
	}
	if job.Cadence != time.Minute || job.ScheduleDelay != 15*time.Second {
		t.Fatalf("schedule = cadence %s delay %s", job.Cadence, job.ScheduleDelay)
	}
	if err := job.validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := job.Handler(t.Context(), HandlerInput{PlanTime: time.Now()})
	if err != nil {
		t.Fatalf("nil dependency handler: %v", err)
	}
	if skipped, _ := result.Result["skipped"].(bool); !skipped {
		t.Fatalf("result = %#v, want skipped", result.Result)
	}
}
