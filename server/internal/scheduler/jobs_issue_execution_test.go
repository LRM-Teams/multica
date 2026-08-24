package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestIssueExecutionReconcileJobContract(t *testing.T) {
	job := IssueExecutionReconcileJob(nil)
	if job.Name != JobNameIssueExecutionReconcile {
		t.Fatalf("job name = %q", job.Name)
	}
	if job.Cadence != 5*time.Second || job.MaxPlansPerTick != 1 || !job.AllowStaleReentry {
		t.Fatalf("unexpected recovery job contract: %#v", job)
	}
	if err := job.validate(); err != nil {
		t.Fatalf("job validation: %v", err)
	}
	result, err := job.Handler(context.Background(), HandlerInput{})
	if err != nil {
		t.Fatalf("nil execution service: %v", err)
	}
	if result.Result["skipped"] != true || result.Result["reason"] != "issue_execution_unavailable" {
		t.Fatalf("nil execution result = %#v", result)
	}
}
