package researchrun

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// A 'ready' V6 work item whose attempt budget is exhausted is invisible to
// both dispatch preparation and lease recovery; the recovery sweep must fail
// it explicitly so the Director learns about the terminal outcome.
func TestRecoverV6ZombieReadyWorkItemFailsBudgetExhausted(t *testing.T) {
	run := newTransactionRecoveryRun(t, "zombie ready work item recovery")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	_, workItemID := seedV6RecoveryWorkItem(t, run, "ready", time.Now().Add(time.Hour))
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item SET attempt_count=max_attempts, ready_at=now() WHERE id=$1::uuid`, workItemID); err != nil {
		t.Fatal(err)
	}
	count, err := run.store.RecoverExpiredV6WorkItems(run.ctx, 10)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least one recovered item, got %d", count)
	}
	var status, reason string
	if err := run.pool.QueryRow(run.ctx, `SELECT status,terminal_reason_code FROM research_work_item WHERE id=$1::uuid`, workItemID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || reason != "attempt_budget_exhausted" {
		t.Fatalf("expected failed/attempt_budget_exhausted, got %s/%s", status, reason)
	}
	var eventCount int
	if err := run.pool.QueryRow(run.ctx, `SELECT count(*) FROM research_run_event
		WHERE session_id=$1::uuid AND event_type='v6_work_item_recovered' AND payload->>'work_item_id'=$2`,
		run.fixture.sessionID, workItemID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("expected one v6_work_item_recovered event, got %d", eventCount)
	}
}

// A running V6 run with nothing in flight and no recent events must be woken
// by an idle Director catch-up cycle; a second sweep must not duplicate it
// while the catch-up cycle's own work item is still active.
func TestProcessV6IdleRunsWakesSilentRun(t *testing.T) {
	run := newTransactionRecoveryRun(t, "idle run watchdog")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: "idle watchdog test", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	// The run has been silent for longer than the idle threshold.
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_run_event SET created_at=now()-interval '11 minutes' WHERE session_id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	woken, err := run.store.ProcessV6IdleRuns(run.ctx, 10)
	if err != nil {
		t.Fatalf("idle sweep: %v", err)
	}
	if woken != 1 {
		t.Fatalf("expected one woken run, got %d", woken)
	}
	var cycleWorkItems int
	if err := run.pool.QueryRow(run.ctx, `SELECT count(*) FROM research_work_item
		WHERE session_id=$1::uuid AND kind='director' AND client_key LIKE 'director-cycle:idle:%'`,
		run.fixture.sessionID).Scan(&cycleWorkItems); err != nil {
		t.Fatal(err)
	}
	if cycleWorkItems != 1 {
		t.Fatalf("expected one idle Director cycle work item, got %d", cycleWorkItems)
	}
	// The catch-up cycle is now the in-flight work; the watchdog must not fire again.
	woken, err = run.store.ProcessV6IdleRuns(run.ctx, 10)
	if err != nil {
		t.Fatalf("second idle sweep: %v", err)
	}
	if woken != 0 {
		t.Fatalf("expected no additional wake, got %d", woken)
	}
}
