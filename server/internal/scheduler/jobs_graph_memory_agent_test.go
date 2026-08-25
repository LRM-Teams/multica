package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestGraphMemoryAgentReconcileJobContract(t *testing.T) {
	job := GraphMemoryAgentReconcileJob(nil, nil)
	if job.Name != JobNameGraphMemoryAgentReconcile {
		t.Fatalf("job name = %q", job.Name)
	}
	if job.Cadence != time.Minute {
		t.Fatalf("cadence = %s, want 1m", job.Cadence)
	}
	if err := job.validate(); err != nil {
		t.Fatalf("job validation: %v", err)
	}
	result, err := job.Handler(context.Background(), HandlerInput{})
	if err != nil {
		t.Fatalf("nil-safe handler: %v", err)
	}
	if result.Result["skipped"] != true {
		t.Fatalf("nil-safe result = %#v", result.Result)
	}
}
