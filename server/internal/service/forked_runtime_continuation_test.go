package service

import (
	"context"
	"fmt"
	"testing"
)

type fakeForkedEnqueuer struct {
	calls   []ChannelRunInput
	indexes []int
	err     error
}

func (f *fakeForkedEnqueuer) EnqueueEnvDispatchChannelRun(_ context.Context, _, _ string, in ChannelRunInput, idx int) (string, error) {
	f.calls = append(f.calls, in)
	f.indexes = append(f.indexes, idx)
	if f.err != nil {
		return "", f.err
	}
	return fmt.Sprintf("run-%d", len(f.calls)), nil
}

func TestForkedRuntimeContinuationEnqueuesAgainstLaneRuntime(t *testing.T) {
	enq := &fakeForkedEnqueuer{}
	strategy := NewForkedRuntimeContinuation(enq)

	if strategy.Mode() != SaveModeSnapshot {
		t.Fatalf("mode = %s, want snapshot", strategy.Mode())
	}
	out, err := strategy.ResumeAgentRun(context.Background(), ContinuationRequest{
		WorkspaceID: "ws",
		ActorUserID: "u",
		Index:       3,
		Trigger:     ResumeTrigger{AgentID: "a-src", Kind: "chat"},
		Lane: LaneRef{
			LaneKey: "lane-0", LaneEnvID: "env-0", InstanceID: "inst-0", ProjectID: "proj-0",
			RuntimeID: "rt-0", AgentID: "a-0", ChannelID: "ch-0",
			ChatSessionID: "cs-0", SourceMessageID: "msg-0",
		},
	})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if out.Status != TriggerExecuted || out.TaskID != "run-1" || out.LaneKey != "lane-0" {
		t.Fatalf("outcome = %+v", out)
	}
	if len(enq.calls) != 1 {
		t.Fatalf("enqueue calls = %d, want 1", len(enq.calls))
	}
	got := enq.calls[0]
	if got.RuntimeID != "rt-0" || got.SandboxInstanceID != "inst-0" || got.AgentID != "a-0" {
		t.Fatalf("lane binding not used: %+v", got)
	}
	// The env id is its own field: a fan-out lane key is an anchor plus an
	// ordinal, so using the lane key as the env id would enqueue against a
	// nonexistent env.
	if got.EnvID != "env-0" {
		t.Fatalf("enqueued env = %q, want the lane's env id", got.EnvID)
	}
	// The caller's rollout index must survive the seam, not be flattened to 0.
	if enq.indexes[0] != 3 {
		t.Fatalf("enqueue index = %d, want 3", enq.indexes[0])
	}
}

func TestForkedRuntimeContinuationRejectsMissingLaneRuntime(t *testing.T) {
	enq := &fakeForkedEnqueuer{}
	strategy := NewForkedRuntimeContinuation(enq)
	out, err := strategy.ResumeAgentRun(context.Background(), ContinuationRequest{
		WorkspaceID: "ws",
		Lane:        LaneRef{LaneKey: "lane-0", InstanceID: "inst-0"},
	})
	if err == nil {
		t.Fatal("expected error when the lane has no runtime")
	}
	if out.Status != TriggerFailed {
		t.Fatalf("status = %s, want failed", out.Status)
	}
	if len(enq.calls) != 0 {
		t.Fatalf("an unbound lane must not be enqueued, got %d calls", len(enq.calls))
	}
}

// A lane with no env id would start a run detached from the environment it is
// meant to act on, which the enqueue path cannot detect for itself.
func TestForkedRuntimeContinuationRejectsMissingLaneEnvID(t *testing.T) {
	enq := &fakeForkedEnqueuer{}
	strategy := NewForkedRuntimeContinuation(enq)
	out, err := strategy.ResumeAgentRun(context.Background(), ContinuationRequest{
		WorkspaceID: "ws",
		Lane:        LaneRef{LaneKey: "lane-0", InstanceID: "i", RuntimeID: "rt", AgentID: "a"},
	})
	if err == nil {
		t.Fatal("expected error when the lane has no env id")
	}
	if out.Status != TriggerFailed {
		t.Fatalf("status = %s, want failed", out.Status)
	}
	if len(enq.calls) != 0 {
		t.Fatalf("an env-less lane must not be enqueued, got %d calls", len(enq.calls))
	}
}

func TestForkedRuntimeContinuationReportsEnqueueFailureAsFailed(t *testing.T) {
	enq := &fakeForkedEnqueuer{err: fmt.Errorf("queue down")}
	strategy := NewForkedRuntimeContinuation(enq)
	out, err := strategy.ResumeAgentRun(context.Background(), ContinuationRequest{
		WorkspaceID: "ws",
		Lane:        LaneRef{LaneKey: "lane-0", LaneEnvID: "env-0", InstanceID: "i", RuntimeID: "rt", AgentID: "a", ChannelID: "ch"},
	})
	if err == nil {
		t.Fatal("expected enqueue error")
	}
	if out.Status != TriggerFailed || out.LaneKey != "lane-0" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestForkedRuntimeContinuationRejectsMissingEnqueuer(t *testing.T) {
	strategy := NewForkedRuntimeContinuation(nil)
	out, err := strategy.ResumeAgentRun(context.Background(), ContinuationRequest{
		Lane: LaneRef{LaneKey: "lane-0", InstanceID: "i", RuntimeID: "rt", AgentID: "a"},
	})
	if err == nil {
		t.Fatal("expected error when no enqueuer is configured")
	}
	if out.Status != TriggerFailed {
		t.Fatalf("status = %s, want failed", out.Status)
	}
}
