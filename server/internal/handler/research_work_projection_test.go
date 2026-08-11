package handler

import (
	"encoding/json"
	"sync"
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

func TestBuildWorkTimeline_ReportKindForSynthesisTask(t *testing.T) {
	t0 := researchProjTime(2026, 8, 10, 8, 0)
	t1 := researchProjTime(2026, 8, 10, 8, 12)
	task := researchrun.Task{
		ID: "task-syn", Kind: researchrun.TaskKindSynthesize,
		Status: researchrun.TaskStatusSucceeded, AssignedAgentID: "agent-s",
		MaxAttempts: 1, AttemptCount: 1, StartedAt: &t0, CompletedAt: &t1,
	}
	evs := buildWorkTimeline(&task, nil, &researchrun.RunSnapshot{})
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	// dispatch, report(start), report(completion), complete
	want := []string{"dispatch", "report", "report", "complete"}
	if len(kinds) != len(want) {
		t.Fatalf("timeline kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("timeline kinds = %v, want %v at %d", kinds, want, i)
		}
	}
}

func TestWorkProjectionEntry_ContractFieldSemantics(t *testing.T) {
	now := researchProjTime(2026, 8, 10, 9, 0)
	cases := []struct {
		name         string
		task         researchrun.Task
		claims       []researchrun.Claim
		sources      []researchrun.SourceSnapshotView
		wantLatest   string
		wantEvidence int
		wantBlocked  string
		wantFailed   string
		wantNextStep string
		wantPct      bool
	}{
		{
			name:    "pending empty contract",
			task:    researchrun.Task{ID: "t-p", Status: researchrun.TaskStatusPending, MaxAttempts: 2, AttemptCount: 0, Kind: researchrun.TaskKindPlan},
			wantPct: false,
		},
		{
			name:         "running has finding + no fabricated blocked/failed",
			task:         researchrun.Task{ID: "t-r", Status: researchrun.TaskStatusRunning, MaxAttempts: 3, AttemptCount: 1, Kind: researchrun.TaskKindDeepRead, StartedAt: &now},
			claims:       []researchrun.Claim{{ID: "c1", ProducedByTaskID: "t-r", Text: "mid-way finding", CreatedAt: now, UpdatedAt: now}},
			sources:      []researchrun.SourceSnapshotView{{ID: "s1", ProducedByTaskID: "t-r", Title: "src", RetrievedAt: now}},
			wantLatest:   "mid-way finding",
			wantEvidence: 2,
			wantPct:      true,
		},
		{
			name:         "blocked surfaces reason + next step",
			task:         researchrun.Task{ID: "t-b", Status: researchrun.TaskStatusBlocked, TerminalReason: "provider down", MaxAttempts: 2, AttemptCount: 1, Kind: researchrun.TaskKindVerify, CompletedAt: &now},
			wantBlocked:  "provider down",
			wantFailed:   "",
			wantNextStep: "等待重派或因阻塞解除后重试",
			wantPct:      true, // started + count>0 qualifies pct; blocked still shows progress done so far
		},
		{
			name:         "failed exposes reason + retry-eligible next step",
			task:         researchrun.Task{ID: "t-f", Status: researchrun.TaskStatusFailed, TerminalReason: "exhausted", MaxAttempts: 3, AttemptCount: 2, Kind: researchrun.TaskKindDiscover, CompletedAt: &now},
			wantFailed:   "exhausted",
			wantBlocked:  "",
			wantNextStep: "将自动重试或重新派发",
			wantPct:      true,
		},
		{
			name:         "failed exhausted next step asks human",
			task:         researchrun.Task{ID: "t-fe", Status: researchrun.TaskStatusFailed, TerminalReason: "out", MaxAttempts: 2, AttemptCount: 2, Kind: researchrun.TaskKindDiscover, CompletedAt: &now},
			wantFailed:   "out",
			wantNextStep: "已用尽重试次数，等待人工介入",
			wantPct:      true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := researchrun.RunSnapshot{Tasks: []researchrun.Task{c.task}, Claims: c.claims, Sources: c.sources}
			mockAttempts := []researchrun.Attempt{}
			entry := buildWorkProjectionEntry(&c.task, mockAttempts, &snap)
			if entry.LatestFinding != c.wantLatest {
				t.Errorf("latest_finding = %q, want %q", entry.LatestFinding, c.wantLatest)
			}
			if entry.EvidenceCount != c.wantEvidence {
				t.Errorf("evidence_count = %d, want %d", entry.EvidenceCount, c.wantEvidence)
			}
			if entry.BlockedReason != c.wantBlocked {
				t.Errorf("blocked_reason = %q, want %q", entry.BlockedReason, c.wantBlocked)
			}
			if entry.FailureReason != c.wantFailed {
				t.Errorf("failure_reason = %q, want %q", entry.FailureReason, c.wantFailed)
			}
			if entry.NextStep != c.wantNextStep {
				t.Errorf("next_step = %q, want %q", entry.NextStep, c.wantNextStep)
			}
			if c.wantPct && entry.ProgressPercent == nil {
				t.Errorf("expected progress_percent to be present")
			}
			if !c.wantPct && entry.ProgressPercent != nil {
				t.Errorf("progress_percent = %d, want absent", *entry.ProgressPercent)
			}
		})
	}
}

func TestWorkProjectionNotifier_CoalescesBurstIntoOneNudge(t *testing.T) {
	var mu sync.Mutex
	var got []struct {
		key string
		seq int64
	}
	publish := func(key string, seq int64) {
		mu.Lock()
		got = append(got, struct {
			key string
			seq int64
		}{key, seq})
		mu.Unlock()
	}
	n := newWorkProjectionNotifier(time.Hour, publish) // long interval: no timer fires
	defer n.Close()

	// A burst of lifecycle events for the same session within the throttle
	// window must collapse to exactly one nudge.
	for i := 0; i < 20; i++ {
		n.Notify("ws|session-1")
	}
	n.Flush()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 coalesced nudge, got %d: %+v", len(got), got)
	}
	if got[0].key != "ws|session-1" {
		t.Errorf("nudge key = %q, want ws|session-1", got[0].key)
	}
	if got[0].seq != 1 {
		t.Errorf("nudge seq = %d, want 1", got[0].seq)
	}
}

func TestWorkProjectionNotifier_DistinctScopesCoalesceSeparately(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	publish := func(key string, seq int64) {
		mu.Lock()
		keys = append(keys, key)
		mu.Unlock()
	}
	n := newWorkProjectionNotifier(time.Hour, publish)
	defer n.Close()
	for i := 0; i < 5; i++ {
		n.Notify("ws|session-a")
		n.Notify("ws|session-b")
		n.Notify("other-ws|session-a") // same session id, different workspace => distinct scope
	}
	n.Flush()
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 3 {
		t.Fatalf("expected 3 nudges (3 distinct scopes), got %d: %v", len(keys), keys)
	}
}

func TestWorkProjectionNotifier_SequenceIsMonotonicAcrossWindows(t *testing.T) {
	var mu sync.Mutex
	var seqs []int64
	publish := func(_ string, seq int64) {
		mu.Lock()
		seqs = append(seqs, seq)
		mu.Unlock()
	}
	n := newWorkProjectionNotifier(time.Hour, publish)
	defer n.Close()

	// First burst -> seq 1, second burst -> seq 2: monotonic per session so a
	// reconnecting client can detect gaps and refetch the HTTP snapshot.
	n.Notify("ws|session-1")
	n.Flush()
	n.Notify("ws|session-1")
	n.Flush()
	n.Notify("ws|session-1")
	n.Flush()

	mu.Lock()
	defer mu.Unlock()
	want := []int64{1, 2, 3}
	if len(seqs) != len(want) {
		t.Fatalf("seqs = %v, want %v", seqs, want)
	}
	for i := range want {
		if seqs[i] != want[i] {
			t.Fatalf("seqs = %v, want %v at %d", seqs, want, i)
		}
	}
}

func TestScopeKeyForWorkProjectionIsolation(t *testing.T) {
	if scopeKeyForWorkProjection("ws-a", "s-1") == scopeKeyForWorkProjection("ws-b", "s-1") {
		t.Fatal("same session id in different workspaces must not share a nudge scope")
	}
	if scopeKeyForWorkProjection("ws-a", "s-1") == scopeKeyForWorkProjection("ws-a", "s-2") {
		t.Fatal("different sessions in same workspace must not share a nudge scope")
	}
}

