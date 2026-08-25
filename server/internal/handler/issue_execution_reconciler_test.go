package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestIssueExecutionReassignmentHandsOffLeaseAndFencesStartedDelivery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "canonical reassignment runtime")
	agentA, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical owner a")
	agentB, unusedIssueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical owner b")
	_ = unusedIssueID

	if _, err := testPool.Exec(ctx, `
		UPDATE issue
		SET status='todo', assignee_type='agent', assignee_id=$2
		WHERE id=$1`, issueID, agentA); err != nil {
		t.Fatalf("assign initial agent: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	first, err := testHandler.IssueExecution.Reconcile(ctx, issue.WorkspaceID, issue.ID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_initial_assignment",
	})
	if err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	if !first.Dispatch || !first.RunID.Valid {
		t.Fatalf("initial reconcile outcome = %#v", first)
	}
	if _, err := testHandler.IssueExecution.DispatchPending(ctx, 100); err != nil {
		t.Fatalf("drain canonical dispatch outbox: %v", err)
	}

	delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, mustGetAgentRuntime(t, runtimeID))
	if err != nil {
		t.Fatalf("lease first canonical run: %v", err)
	}

	current, err := testHandler.Queries.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := testHandler.IssueExecution.UpdateIssue(ctx, db.UpdateIssueParams{
		ID:            current.ID,
		AssigneeType:  pgtype.Text{String: "agent", Valid: true},
		AssigneeID:    parseUUID(agentB),
		StartDate:     current.StartDate,
		DueDate:       current.DueDate,
		ParentIssueID: current.ParentIssueID,
		ProjectID:     current.ProjectID,
	}, current.WorkspaceID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_reassignment", Invalidate: true,
	})
	if err != nil {
		t.Fatalf("reassign issue: %v", err)
	}
	if updated.AssigneeID != parseUUID(agentB) {
		t.Fatalf("assignee = %s, want %s", updated.AssigneeID.String(), agentB)
	}

	claim, err := testHandler.Queries.GetActiveIssueExecution(ctx, db.GetActiveIssueExecutionParams{
		WorkspaceID: current.WorkspaceID, IssueID: current.ID,
	})
	if err != nil {
		t.Fatalf("load replacement claim: %v", err)
	}
	if claim.AgentID != parseUUID(agentB) || claim.RunID == first.RunID {
		t.Fatalf("replacement claim = %#v", claim)
	}
	var leaseOwner string
	if err := testPool.QueryRow(ctx, `
		SELECT owner_agent_id::text FROM work_owner_lease
		WHERE workspace_id=$1 AND issue_id=$2 AND role='executor' AND status='active'`,
		current.WorkspaceID, current.ID).Scan(&leaseOwner); err != nil {
		t.Fatalf("load active executor lease: %v", err)
	}
	if leaseOwner != agentB {
		t.Fatalf("active lease owner = %s, want %s", leaseOwner, agentB)
	}

	_, err = testHandler.TaskService.StartAgentInboxTask(ctx,
		pgtype.UUID{Bytes: uuid.New(), Valid: true},
		service.AgentInboxDeliveryFence{
			DeliveryID: delivery.ID, InboxEventID: delivery.InboxEventID, LeaseToken: delivery.LeaseToken,
		})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("superseded leased run provider start error = %v, want pgx.ErrNoRows", err)
	}
	var oldStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id=$1`, first.RunID).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "suppressed" {
		t.Fatalf("old run status = %q, want suppressed", oldStatus)
	}
}

func TestIssueExecutionRecoveryReplacesLegacyAssignmentWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "canonical legacy recovery runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical legacy recovery")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status='todo', assignee_type='agent', assignee_id=$2 WHERE id=$1`,
		issueID, agentID); err != nil {
		t.Fatal(err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := testHandler.TaskService.EnqueueTaskForIssue(ctx, issue)
	if err != nil {
		t.Fatalf("seed legacy wake: %v", err)
	}
	if legacy.IssueRunKind.Valid {
		t.Fatalf("legacy wake unexpectedly canonical: %#v", legacy.IssueRunKind)
	}

	outcome, err := testHandler.IssueExecution.Reconcile(ctx, issue.WorkspaceID, issue.ID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_legacy_recovery",
	})
	if err != nil {
		t.Fatalf("recover legacy wake: %v", err)
	}
	if !outcome.Dispatch {
		t.Fatalf("recovery did not create canonical dispatch: %#v", outcome)
	}
	var legacyStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id=$1`, legacy.ID).Scan(&legacyStatus); err != nil {
		t.Fatal(err)
	}
	if legacyStatus != "suppressed" {
		t.Fatalf("legacy wake status = %q, want suppressed", legacyStatus)
	}
	var canonicalCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event
		WHERE issue_id=$1 AND issue_run_kind='canonical' AND status IN ('pending','draining','failed')`,
		issue.ID).Scan(&canonicalCount); err != nil {
		t.Fatal(err)
	}
	if canonicalCount != 1 {
		t.Fatalf("active canonical run count = %d, want 1", canonicalCount)
	}
}

func TestIssueExecutionRecoveryScannerWaitsForActiveLegacyAssignment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "canonical legacy rollout runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical legacy rollout")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status='todo', assignee_type='agent', assignee_id=$2 WHERE id=$1`,
		issueID, agentID); err != nil {
		t.Fatal(err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := testHandler.TaskService.EnqueueTaskForIssue(ctx, issue)
	if err != nil {
		t.Fatalf("seed active legacy wake: %v", err)
	}

	if _, err = testHandler.IssueExecution.RecoverMissing(ctx, 100); err != nil {
		t.Fatalf("scan with active legacy wake: %v", err)
	}
	var canonicalCount int
	if err = testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event
		WHERE issue_id=$1 AND issue_run_kind='canonical'`, issue.ID).Scan(&canonicalCount); err != nil {
		t.Fatal(err)
	}
	if canonicalCount != 0 {
		t.Fatalf("canonical runs while legacy wake active = %d, want 0", canonicalCount)
	}
	currentLegacy, err := testHandler.Queries.GetAgentInboxEvent(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if currentLegacy.Status != "pending" {
		t.Fatalf("legacy wake status = %q, want pending", currentLegacy.Status)
	}

	if _, err = testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status='acked', terminal_outcome='completed', terminal_at=now(), completed_at=now()
		WHERE id=$1`, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = testHandler.IssueExecution.RecoverMissing(ctx, 100); err != nil {
		t.Fatalf("scan after legacy wake terminal: %v", err)
	}
	if err = testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event
		WHERE issue_id=$1 AND issue_run_kind='canonical'
		  AND status IN ('pending','draining','failed')`, issue.ID).Scan(&canonicalCount); err != nil {
		t.Fatal(err)
	}
	if canonicalCount != 1 {
		t.Fatalf("canonical runs after legacy wake terminal = %d, want 1", canonicalCount)
	}
}

func TestWorkGraphReadyTransactionSurvivesPostCommitWakeCrash(t *testing.T) {
	if testHandler == nil || testPool == nil || testHandler.WorkGraph == nil || testHandler.WorkGraph.OnNodesReadyTx == nil {
		t.Skip("database or work graph not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "graph ready crash runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "graph ready crash recovery")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status='backlog', assignee_type='agent', assignee_id=$2 WHERE id=$1`,
		issueID, agentID); err != nil {
		t.Fatal(err)
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = testHandler.WorkGraph.OnNodesReadyTx(ctx, tx, testWorkspaceID, []string{issueID}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("persist graph ready intent: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != "todo" {
		t.Fatalf("ready issue status = %q, want todo", issue.Status)
	}
	var pendingOutbox, canonicalEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM issue_dispatch_outbox
		WHERE issue_id=$1 AND status='pending'`, issue.ID).Scan(&pendingOutbox); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event
		WHERE issue_id=$1 AND issue_run_kind='canonical'`, issue.ID).Scan(&canonicalEvents); err != nil {
		t.Fatal(err)
	}
	if pendingOutbox != 1 || canonicalEvents != 0 {
		t.Fatalf("post-commit crash boundary outbox/events = %d/%d, want 1/0", pendingOutbox, canonicalEvents)
	}
	processed, err := testHandler.IssueExecution.RecoverMissing(ctx, 10)
	if err != nil {
		t.Fatalf("recover durable graph ready intent: %v", err)
	}
	if processed < 1 {
		t.Fatalf("recovery processed = %d, want at least 1", processed)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event
		WHERE issue_id=$1 AND issue_run_kind='canonical' AND status='pending'`, issue.ID).Scan(&canonicalEvents); err != nil {
		t.Fatal(err)
	}
	if canonicalEvents != 1 {
		t.Fatalf("recovered canonical events = %d, want 1", canonicalEvents)
	}
}

func TestIssueExecutionAdoptsAuthenticatedCurrentRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "canonical adopt runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical adopt current run")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status='todo', assignee_type='agent', assignee_id=$2 WHERE id=$1`,
		issueID, agentID); err != nil {
		t.Fatal(err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	queued, err := testHandler.IssueExecution.Reconcile(ctx, issue.WorkspaceID, issue.ID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_adopt_initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	var currentRunID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, runtime_id, issue_id, reason, status, started_at
		) VALUES ($1, $2, $3, $4, 'issue', 'draining', now())
		RETURNING id`, issue.WorkspaceID, agentID, runtimeID, issue.ID).Scan(&currentRunID); err != nil {
		t.Fatal(err)
	}

	outcome, err := testHandler.IssueExecution.Reconcile(ctx, issue.WorkspaceID, issue.ID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_adopt_current", Invalidate: true, PreserveRunID: parseUUID(currentRunID),
	})
	if err != nil {
		t.Fatalf("adopt current run: %v", err)
	}
	if outcome.Dispatch {
		t.Fatalf("adoption dispatched a duplicate run: %#v", outcome)
	}
	claim, err := testHandler.Queries.GetActiveIssueExecution(ctx, db.GetActiveIssueExecutionParams{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.RunID != parseUUID(currentRunID) || claim.Status != "active" {
		t.Fatalf("adopted claim = %#v", claim)
	}
	current, err := testHandler.Queries.GetAgentInboxEvent(ctx, parseUUID(currentRunID))
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "draining" || current.IssueRunKind.String != "canonical" ||
		current.IssueExecutionAttemptNumber.Int64 != claim.AttemptNumber {
		t.Fatalf("adopted event = %#v", current)
	}
	old, err := testHandler.Queries.GetAgentInboxEvent(ctx, queued.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "suppressed" {
		t.Fatalf("replaced queued run status = %q, want suppressed", old.Status)
	}
}

func TestDeliveredIssueOutboxRetainsAuditAfterInboxRunCleanup(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "canonical outbox retention runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical outbox retention")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status='todo', assignee_type='agent', assignee_id=$2 WHERE id=$1`,
		issueID, agentID); err != nil {
		t.Fatal(err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := testHandler.IssueExecution.Reconcile(ctx, issue.WorkspaceID, issue.ID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_outbox_retention",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = testHandler.IssueExecution.DispatchRun(ctx, issue.WorkspaceID, outcome.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id=$1`, outcome.RunID); err != nil {
		t.Fatalf("delete retained Inbox Run: %v", err)
	}
	var status string
	var deliveredEventID pgtype.UUID
	var deliveredAt pgtype.Timestamptz
	if err = testPool.QueryRow(ctx, `
		SELECT status, delivered_event_id, delivered_at
		FROM issue_dispatch_outbox WHERE run_id=$1`, outcome.RunID,
	).Scan(&status, &deliveredEventID, &deliveredAt); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" || deliveredEventID.Valid || !deliveredAt.Valid {
		t.Fatalf("retained outbox audit = status %q event %#v delivered_at %#v", status, deliveredEventID, deliveredAt)
	}
}

func TestIssueExecutionRunnableStatusMatrixAndDependencyGate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "canonical status matrix runtime")
	statuses := []struct {
		status   string
		runnable bool
	}{
		{status: "backlog"},
		{status: "todo", runnable: true},
		{status: "in_progress", runnable: true},
		{status: "in_review"},
		{status: "done"},
		{status: "blocked"},
		{status: "cancelled"},
	}
	for _, tc := range statuses {
		t.Run(tc.status, func(t *testing.T) {
			agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical status "+tc.status)
			if _, err := testPool.Exec(ctx, `
				UPDATE issue SET status=$2, assignee_type='agent', assignee_id=$3 WHERE id=$1`,
				issueID, tc.status, agentID); err != nil {
				t.Fatal(err)
			}
			issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := testHandler.IssueExecution.Reconcile(ctx, issue.WorkspaceID, issue.ID, service.IssueExecutionReconcileOptions{
				TriggerKind: "test_status_matrix",
			})
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Dispatch != tc.runnable {
				t.Fatalf("status %s dispatch=%v, want %v", tc.status, outcome.Dispatch, tc.runnable)
			}
		})
	}

	agentID, blockedIssueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical dependency blocked")
	_, upstreamIssueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical dependency upstream")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status='todo', assignee_type='agent', assignee_id=$2 WHERE id=$1`,
		blockedIssueID, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status='todo' WHERE id=$1`, upstreamIssueID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_dependency(issue_id, depends_on_issue_id, type) VALUES($1, $2, 'blocked_by')`,
		blockedIssueID, upstreamIssueID); err != nil {
		t.Fatal(err)
	}
	blockedIssue, err := testHandler.Queries.GetIssue(ctx, parseUUID(blockedIssueID))
	if err != nil {
		t.Fatal(err)
	}
	blockedOutcome, err := testHandler.IssueExecution.Reconcile(ctx, blockedIssue.WorkspaceID, blockedIssue.ID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_dependency_blocked",
	})
	if err != nil {
		t.Fatal(err)
	}
	if blockedOutcome.Dispatch {
		t.Fatal("dependency-blocked issue dispatched")
	}
	if _, err := testPool.Exec(ctx, `UPDATE issue SET status='done' WHERE id=$1`, upstreamIssueID); err != nil {
		t.Fatal(err)
	}
	readyOutcome, err := testHandler.IssueExecution.Reconcile(ctx, blockedIssue.WorkspaceID, blockedIssue.ID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_dependency_ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !readyOutcome.Dispatch {
		t.Fatal("dependency-ready issue did not dispatch")
	}
}

func TestAcquireWorkOwnerLeaseRejectsCrossWorkspaceIssue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "lease tenant guard runtime")
	agentID, _ := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "lease tenant guard agent")
	var foreignWorkspaceID, foreignIssueID string
	suffix := uuid.NewString()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace(name, slug) VALUES($1, $2) RETURNING id`,
		"lease tenant guard", "lease-tenant-guard-"+suffix).Scan(&foreignWorkspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id=$1`, foreignWorkspaceID)
	})
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue(workspace_id,title,status,creator_type,creator_id)
		VALUES($1,'foreign lease issue','todo','member',$2) RETURNING id`,
		foreignWorkspaceID, testUserID).Scan(&foreignIssueID); err != nil {
		t.Fatal(err)
	}
	_, err := testHandler.acquireWorkOwnerLease(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), workOwnerLeaseAcquireRequest{
		IssueID: foreignIssueID,
	})
	if err == nil {
		t.Fatal("cross-workspace issue lease unexpectedly succeeded")
	}
	var count int
	if scanErr := testPool.QueryRow(ctx, `SELECT count(*) FROM work_owner_lease WHERE issue_id=$1`, foreignIssueID).Scan(&count); scanErr != nil {
		t.Fatal(scanErr)
	}
	if count != 0 {
		t.Fatalf("cross-workspace lease rows = %d, want 0", count)
	}
}

func TestCanonicalIssueManualRerunAndInfrastructureRetryCreateRunLineage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "canonical retry runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical retry")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status='todo', assignee_type='agent', assignee_id=$2 WHERE id=$1`,
		issueID, agentID); err != nil {
		t.Fatal(err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := testHandler.IssueExecution.Reconcile(ctx, issue.WorkspaceID, issue.ID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_retry_initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	manual, err := testHandler.TaskService.RerunIssue(ctx, issue.ID, initial.RunID, pgtype.UUID{})
	if err != nil {
		t.Fatalf("manual canonical rerun: %v", err)
	}
	if manual.IssueRunKind.String != "canonical" || manual.ParentTaskID != initial.RunID || !manual.ForceFreshSession {
		t.Fatalf("manual rerun contract = %#v", manual)
	}
	if manual.IssueExecutionRevision.Int64 != 0 || manual.IssueExecutionAttemptNumber.Int64 != 2 {
		t.Fatalf("manual rerun revision/attempt = %d/%d, want 0/2",
			manual.IssueExecutionRevision.Int64, manual.IssueExecutionAttemptNumber.Int64)
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status='acked', terminal_outcome='failed', terminal_at=now(), completed_at=now(),
		    failure_reason='runtime_offline', attempt=1, max_attempts=3,
		    session_id='resume-provider-session', work_dir='/tmp/retry'
		WHERE id=$1`, manual.ID); err != nil {
		t.Fatal(err)
	}
	failed, err := testHandler.Queries.GetAgentInboxEvent(ctx, manual.ID)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := testHandler.TaskService.MaybeRetryFailedTask(ctx, failed)
	if err != nil {
		t.Fatalf("canonical infrastructure retry: %v", err)
	}
	if retry == nil {
		t.Fatal("canonical infrastructure retry was not created")
	}
	if retry.IssueRunKind.String != "canonical" || retry.ParentTaskID != manual.ID || retry.ForceFreshSession {
		t.Fatalf("infrastructure retry contract = %#v", retry)
	}
	if retry.IssueExecutionRevision.Int64 != 0 || retry.IssueExecutionAttemptNumber.Int64 != 3 {
		t.Fatalf("retry revision/attempt = %d/%d, want 0/3",
			retry.IssueExecutionRevision.Int64, retry.IssueExecutionAttemptNumber.Int64)
	}
	if retry.Attempt != 2 || retry.MaxAttempts != 3 {
		t.Fatalf("retry delivery budget = %d/%d, want 2/3", retry.Attempt, retry.MaxAttempts)
	}
	claim, err := testHandler.Queries.GetActiveIssueExecution(ctx, db.GetActiveIssueExecutionParams{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.RunID != retry.ID {
		t.Fatalf("active retry run = %s, want %s", claim.RunID.String(), retry.ID.String())
	}
}

func TestCanonicalIssueRetryBudgetExhaustionBlocksInsteadOfLooping(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "canonical exhausted runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "canonical exhausted retry")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status='in_progress', assignee_type='agent', assignee_id=$2 WHERE id=$1`,
		issueID, agentID); err != nil {
		t.Fatal(err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := testHandler.IssueExecution.Reconcile(ctx, issue.WorkspaceID, issue.ID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_exhausted_initial", DeliveryAttempt: 1, MaxAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := testHandler.IssueExecution.DispatchRun(ctx, issue.WorkspaceID, initial.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status='acked', terminal_outcome='failed', terminal_at=now(), completed_at=now(),
		    failure_reason='runtime_offline'
		WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	failed, err := testHandler.Queries.GetAgentInboxEvent(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	testHandler.TaskService.HandleFailedTasks(ctx, []db.AgentInboxEvent{failed})
	got, err := testHandler.Queries.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "blocked" {
		t.Fatalf("exhausted retry issue status = %q, want blocked", got.Status)
	}
	if _, err := testHandler.Queries.GetActiveIssueExecution(ctx, db.GetActiveIssueExecutionParams{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("exhausted retry active claim error = %v, want no rows", err)
	}
}

func mustGetAgentRuntime(t *testing.T, runtimeID string) db.AgentRuntime {
	t.Helper()
	runtime, err := testHandler.Queries.GetAgentRuntime(context.Background(), parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	return runtime
}
