package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestIsTaskLifecycleEvent(t *testing.T) {
	lifecycle := []string{
		"control_task_created",
		"task_dispatching",
		"task_dispatched",
		"task_started",
		"task_waiting_for_execution_target",
		"task_attempt_failed",
		"task_attempt_cancelling",
		"task_blocked",
		"task_result_accepted",
		"node_command_retry",
		"node_command_reassign",
		"node_command_rollback",
	}
	for _, typ := range lifecycle {
		if !isTaskLifecycleEvent(typ) {
			t.Errorf("isTaskLifecycleEvent(%q) = false, want true", typ)
		}
	}
	nonLifecycle := []string{
		"run_started",
		"run_completed",
		"budget_exhausted",
		"execution_circuit_transition",
		"test_state_change",
		"",
	}
	for _, typ := range nonLifecycle {
		if isTaskLifecycleEvent(typ) {
			t.Errorf("isTaskLifecycleEvent(%q) = true, want false", typ)
		}
	}
}

func TestWorkProjectionStageFor(t *testing.T) {
	cases := map[string]string{
		"pending":     "queued",
		"running":     "executing",
		"succeeded":   "complete",
		"failed":      "failed",
		"blocked":     "blocked",
		"cancelled":   "cancelled",
		"unknown_foo": "unknown_foo",
	}
	for in, want := range cases {
		if got := workProjectionStageFor(in); got != want {
			t.Errorf("workProjectionStageFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkProjectionPayloadString(t *testing.T) {
	if got := workProjectionPayloadString(json.RawMessage(`{"task_id":"t-1","attempt_id":"a-1"}`), "task_id"); got != "t-1" {
		t.Fatalf("task_id = %q, want t-1", got)
	}
	if got := workProjectionPayloadString(json.RawMessage(`{"attempt_id":"a-1"}`), "task_id"); got != "" {
		t.Fatalf("missing task_id should be empty, got %q", got)
	}
	if got := workProjectionPayloadString(nil, "task_id"); got != "" {
		t.Fatalf("nil payload should be empty, got %q", got)
	}
}

func researchProjTime(y, mo, d, h, mi int) time.Time {
	// convenience: fixed UTC timestamps for deterministic ordering
	return time.Date(y, time.Month(mo), d, h, mi, 0, 0, time.UTC)
}

func TestBuildWorkProjectionEntry_SucceededTaskProgressAndEvidence(t *testing.T) {
	t0 := researchProjTime(2026, 8, 10, 8, 0)
	t1 := researchProjTime(2026, 8, 10, 8, 5)
	t2 := researchProjTime(2026, 8, 10, 8, 10)

	task := researchrun.Task{
		ID:                 "task-1",
		Kind:               researchrun.TaskKindDiscover,
		Objective:          "find pricing evidence",
		RequiredCapability: "web",
		ExpectedResult:     "pricing report",
		Status:             researchrun.TaskStatusSucceeded,
		AssignedAgentID:    "agent-a",
		GoalVersion:        2,
		MaxAttempts:        3,
		AttemptCount:       2,
		ReadyAt:            &t0,
		StartedAt:          &t1,
		CompletedAt:        &t2,
	}

	attempts := []researchrun.Attempt{
		{
			ID: "a-1", TaskID: "task-1", AttemptNumber: 1,
			AssignedAgentID: "agent-a", Status: researchrun.AttemptStatusSucceeded,
			DispatchedAt: t1,
		},
	}

	snap := researchrun.RunSnapshot{
		Tasks:    []researchrun.Task{task},
		Attempts: attempts,
		Sources: []researchrun.SourceSnapshotView{
			{ID: "s-1", ProducedByTaskID: "task-1", Title: "competitor pricing", RetrievedAt: t1},
		},
		Claims: []researchrun.Claim{
			{ID: "c-1", ProducedByTaskID: "task-1", Text: "price is 42", Status: researchrun.ClaimStatusSupported, CreatedAt: t1, UpdatedAt: t2},
		},
	}

	entry := buildWorkProjectionEntry(&task, attempts, &snap)

	if entry.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", entry.Status)
	}
	if entry.Stage != "complete" {
		t.Errorf("stage = %q, want complete", entry.Stage)
	}
	if entry.ProgressPercent == nil || *entry.ProgressPercent != 100 {
		t.Errorf("progress_percent = %v, want 100", entry.ProgressPercent)
	}
	if entry.StepsDone != 2 || entry.StepsTotal != 3 {
		t.Errorf("steps = %d/%d, want 2/3", entry.StepsDone, entry.StepsTotal)
	}
	if entry.EvidenceCount != 2 {
		t.Errorf("evidence_count = %d, want 2 (source+claim)", entry.EvidenceCount)
	}
	if entry.LatestFinding != "price is 42" {
		t.Errorf("latest_finding = %q, want %q", entry.LatestFinding, "price is 42")
	}
	if entry.BlockedReason != "" || entry.FailureReason != "" {
		t.Errorf("succeeded task must not report blocked/failure, got block=%q fail=%q", entry.BlockedReason, entry.FailureReason)
	}

	// Timeline must be time-ascending and include dispatch, finding, validation, complete.
	var seq []string
	for _, e := range entry.Timeline {
		seq = append(seq, e.Kind)
	}
	wantSeq := []string{"dispatch", "query", "source_read", "finding", "validation", "complete"}
	if len(seq) != len(wantSeq) {
		t.Fatalf("timeline kinds = %v, want %v", seq, wantSeq)
	}
	for i := range wantSeq {
		if seq[i] != wantSeq[i] {
			t.Fatalf("timeline kinds = %v, want %v at index %d", seq, wantSeq, i)
		}
	}
	for i := 1; i < len(entry.Timeline); i++ {
		if entry.Timeline[i].UnixMs < entry.Timeline[i-1].UnixMs {
			t.Errorf("timeline not ascending at %d", i)
		}
	}
}

func TestBuildWorkProjectionEntry_BlockedTaskTruthful(t *testing.T) {
	tNow := researchProjTime(2026, 8, 10, 9, 0)
	task := researchrun.Task{
		ID: "task-b", Kind: researchrun.TaskKindVerify,
		Status: researchrun.TaskStatusBlocked, TerminalReason: "dep provider unavailable",
		MaxAttempts: 2, AttemptCount: 2,
		StartedAt: &tNow, CompletedAt: &tNow,
	}
	snap := researchrun.RunSnapshot{Tasks: []researchrun.Task{task}}
	entry := buildWorkProjectionEntry(&task, nil, &snap)
	if entry.Status != "blocked" {
		t.Errorf("status = %q, want blocked", entry.Status)
	}
	if entry.BlockedReason != "dep provider unavailable" {
		t.Errorf("blocked_reason = %q", entry.BlockedReason)
	}
	// No fabrication of progress on a terminal-failure state.
	if entry.StepsDone != 2 {
		t.Errorf("steps_done = %d, want 2", entry.StepsDone)
	}
	// Stage vocabulary must map blocked -> blocked.
	if entry.Stage != "blocked" {
		t.Errorf("stage = %q, want blocked", entry.Stage)
	}
}

func TestBuildWorkProjectionEntry_PendingNoPercent(t *testing.T) {
	task := researchrun.Task{
		ID: "task-p", Kind: researchrun.TaskKindSynthesize,
		Status: researchrun.TaskStatusPending, MaxAttempts: 3, AttemptCount: 0,
	}
	snap := researchrun.RunSnapshot{Tasks: []researchrun.Task{task}}
	entry := buildWorkProjectionEntry(&task, nil, &snap)
	if entry.ProgressPercent != nil {
		t.Errorf("pending task must not fabricate a percent, got %v", *entry.ProgressPercent)
	}
	if entry.Stage != "queued" {
		t.Errorf("stage = %q, want queued", entry.Stage)
	}
}

func TestBuildWorkTimeline_RetryAndCancel(t *testing.T) {
	t0 := researchProjTime(2026, 8, 10, 8, 0)
	t1 := researchProjTime(2026, 8, 10, 8, 5)
	t2 := researchProjTime(2026, 8, 10, 8, 6)
	t3 := researchProjTime(2026, 8, 10, 8, 10)

	task := researchrun.Task{
		ID: "task-r", Kind: researchrun.TaskKindDeepRead,
		Status: researchrun.TaskStatusFailed, TerminalReason: "exhausted retries",
		AssignedAgentID: "agent-x", MaxAttempts: 2, AttemptCount: 2,
		StartedAt: &t0, CompletedAt: &t3,
	}
	attempts := []researchrun.Attempt{
		{ID: "a1", TaskID: "task-r", AttemptNumber: 1, AssignedAgentID: "agent-x", Status: researchrun.AttemptStatusFailed, DispatchedAt: t0, FailureClass: "timeout", SourceFailureReason: "no response"},
		{ID: "a2", TaskID: "task-r", AttemptNumber: 2, AssignedAgentID: "agent-x", Status: researchrun.AttemptStatusCancelled, DispatchedAt: t1, CancelRequestedAt: &t2, FailureClass: "cancelled", SourceFailureReason: "user"},
	}
	evs := buildWorkTimeline(&task, attempts, &researchrun.RunSnapshot{})
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	// dispatch, query(startedAt for deep_read), blocked(attempt1 failed), retry(attempt2>1 cancelled), blocked(task failed)
	want := []string{"dispatch", "query", "blocked", "retry", "blocked"}
	if len(kinds) != len(want) {
		t.Fatalf("timeline kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("timeline kinds = %v, want %v at %d", kinds, want, i)
		}
	}
	// The >1 cancelled attempt must surface as a retry event referencing attempt #2.
	retryFound := false
	for _, e := range evs {
		if e.Kind == "retry" {
			retryFound = true
			if e.Attempt == nil || *e.Attempt != 2 {
				t.Errorf("retry attempt = %v, want 2", e.Attempt)
			}
		}
	}
	if !retryFound {
		t.Error("expected a retry event for the cancelled attempt #2")
	}
}

func TestBuildWorkTimeline_SingleCancelledAttemptShowsCancel(t *testing.T) {
	t0 := researchProjTime(2026, 8, 10, 8, 0)
	task := researchrun.Task{
		ID: "task-c", Kind: researchrun.TaskKindDiscover,
		Status: researchrun.TaskStatusCancelled, AssignedAgentID: "agent-y",
		MaxAttempts: 2, AttemptCount: 1, StartedAt: &t0,
	}
	attempts := []researchrun.Attempt{
		{ID: "a1", TaskID: "task-c", AttemptNumber: 1, AssignedAgentID: "agent-y", Status: researchrun.AttemptStatusCancelled, DispatchedAt: t0, CancelRequestedAt: &t0},
	}
	evs := buildWorkTimeline(&task, attempts, &researchrun.RunSnapshot{})
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	// dispatch, query, cancel (single attempt#1 cancelled)
	want := []string{"dispatch", "query", "cancel"}
	if len(kinds) != len(want) {
		t.Fatalf("timeline kinds = %v, want %v", kinds, want)
	}
	for _, e := range evs {
		if e.Kind == "cancel" && (e.Attempt == nil || *e.Attempt != 1) {
			t.Errorf("cancel attempt = %v, want 1", e.Attempt)
		}
	}
}
