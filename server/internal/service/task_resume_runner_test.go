package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	resumeTestTaskID    = "00000000-0000-0000-0000-000000000001"
	resumeTestRuntimeID = "00000000-0000-0000-0000-000000000002"
)

type fakeInFlightResetter struct {
	task   db.AgentInboxEvent
	err    error
	called bool
}

func (f *fakeInFlightResetter) ResetInFlightTaskForResume(_ context.Context, _ db.ResetInFlightTaskForResumeParams) (db.AgentInboxEvent, error) {
	f.called = true
	return f.task, f.err
}

type fakeWaker struct {
	notified []string
}

func (f *fakeWaker) NotifyTaskAvailable(runtimeID, taskID string) {
	f.notified = append(f.notified, runtimeID+":"+taskID)
}

func TestResumeAgentRunReactivatesExistingTask(t *testing.T) {
	taskID := util.MustParseUUID(resumeTestTaskID)
	runtimeID := util.MustParseUUID(resumeTestRuntimeID)
	resetter := &fakeInFlightResetter{task: db.AgentInboxEvent{ID: taskID, RuntimeID: runtimeID}}
	waker := &fakeWaker{}
	runner := NewTaskResumeRunner(resetter, waker)

	if err := runner.ResumeAgentRun(context.Background(), ResumeTrigger{
		TaskID:    resumeTestTaskID,
		RuntimeID: resumeTestRuntimeID,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resetter.called {
		t.Fatal("reset not called")
	}
	if len(waker.notified) != 1 {
		t.Fatalf("expected 1 wake, got %d", len(waker.notified))
	}
	want := util.UUIDToString(runtimeID) + ":" + util.UUIDToString(taskID)
	if waker.notified[0] != want {
		t.Fatalf("wake = %q, want %q", waker.notified[0], want)
	}
}

func TestResumeAgentRunRejectsTerminalTask(t *testing.T) {
	resetter := &fakeInFlightResetter{err: pgx.ErrNoRows}
	waker := &fakeWaker{}
	runner := NewTaskResumeRunner(resetter, waker)

	err := runner.ResumeAgentRun(context.Background(), ResumeTrigger{
		TaskID:    resumeTestTaskID,
		RuntimeID: resumeTestRuntimeID,
	})
	if !errors.Is(err, ErrTriggerTaskNotResumable) {
		t.Fatalf("expected ErrTriggerTaskNotResumable, got %v", err)
	}
	if len(waker.notified) != 0 {
		t.Fatalf("terminal task must not wake runtime, got %d wakes", len(waker.notified))
	}
}

func TestResumeAgentRunRejectsInvalidTaskID(t *testing.T) {
	resetter := &fakeInFlightResetter{}
	runner := NewTaskResumeRunner(resetter, &fakeWaker{})

	err := runner.ResumeAgentRun(context.Background(), ResumeTrigger{
		TaskID:    "not-a-uuid",
		RuntimeID: resumeTestRuntimeID,
	})
	if err == nil {
		t.Fatal("expected error for invalid task_id, got nil")
	}
	if errors.Is(err, ErrTriggerTaskNotResumable) {
		t.Fatalf("invalid uuid should be a parse error, not ErrTriggerTaskNotResumable: %v", err)
	}
	if resetter.called {
		t.Fatal("reset must not be called when task_id is invalid")
	}
}
