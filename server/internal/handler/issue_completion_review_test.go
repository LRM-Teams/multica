package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func prepareCompletionRun(t *testing.T, name string) (db.Issue, string, service.IssueExecutionReconcileOutcome) {
	t.Helper()
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, name+" runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, name)
	criteria := `[
		"旧世界与新世界地形连续且树木完整",
		"CI、Pages 和 man A-F 视觉验收全部通过"
	]`
	if _, err := testPool.Exec(ctx, `
		UPDATE issue
		SET status='in_progress', assignee_type='agent', assignee_id=$2,
		    acceptance_criteria=$3::jsonb
		WHERE id=$1`, issueID, agentID, criteria); err != nil {
		t.Fatal(err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := testHandler.IssueExecution.Reconcile(ctx, issue.WorkspaceID, issue.ID, service.IssueExecutionReconcileOptions{
		TriggerKind: "test_completion_initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = testHandler.IssueExecution.DispatchRun(ctx, issue.WorkspaceID, outcome.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err = testPool.Exec(ctx, `UPDATE agent_inbox_event SET status='draining', started_at=now() WHERE id=$1`, outcome.RunID); err != nil {
		t.Fatal(err)
	}
	return issue, agentID, outcome
}

func webGameCompletionInput(issue db.Issue, agentID string, runID string) service.SubmitIssueCompletionInput {
	return service.SubmitIssueCompletionInput{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, RunID: parseUUID(runID), AgentID: parseUUID(agentID),
		ExpectedExecutionRevision: issue.ExecutionRevision,
		Summary:                   "恢复旧版视觉基线，并以真实线上截图独立验收。",
		AcceptanceResults: []service.CompletionAcceptanceResult{
			{CriterionIndex: 0, Criterion: "旧世界与新世界地形连续且树木完整", Satisfied: true, EvidenceRefs: []service.CompletionEvidenceRef{{Kind: "screenshot", Ref: "artifact://old-new-world-parity"}}},
			{CriterionIndex: 1, Criterion: "CI、Pages 和 man A-F 视觉验收全部通过", Satisfied: true, EvidenceRefs: []service.CompletionEvidenceRef{{Kind: "test", Ref: "ci:green"}, {Kind: "pull_request", Ref: "https://github.com/example/game/pull/65"}}},
		},
		ArtifactRefs: []service.CompletionEvidenceRef{{Kind: "pull_request", Ref: "https://github.com/example/game/pull/65"}},
		Risks:        []string{"canonical PR link must exist before acceptance"},
	}
}

func TestIssueCompletionRequiresCanonicalPREvidenceBeforeAcceptedReview(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issue, agentID, run := prepareCompletionRun(t, "web game completion")
	submitted, err := testHandler.IssueExecution.SubmitCompletion(ctx, webGameCompletionInput(issue, agentID, uuidToString(run.RunID)))
	if err != nil {
		t.Fatalf("submit completion: %v", err)
	}
	if submitted.Issue.Status != "in_review" || submitted.Issue.ExecutionRevision != issue.ExecutionRevision+1 {
		t.Fatalf("completion Issue status/revision = %s/%d, want in_review/%d",
			submitted.Issue.Status, submitted.Issue.ExecutionRevision, issue.ExecutionRevision+1)
	}
	if !submitted.Report.VisibleCommentID.Valid {
		t.Fatal("completion report has no visible comment")
	}
	var runStatus string
	if err = testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id=$1`, run.RunID).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != "draining" {
		t.Fatalf("completion forged current Run terminal status %q, want draining", runStatus)
	}
	if _, err = testHandler.Queries.GetActiveIssueExecution(ctx, db.GetActiveIssueExecutionParams{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("active executor claim after completion = %v, want none", err)
	}
	completionReplay, err := testHandler.IssueExecution.SubmitCompletion(ctx, webGameCompletionInput(issue, agentID, uuidToString(run.RunID)))
	if err != nil || !completionReplay.Replayed || completionReplay.Report.ID != submitted.Report.ID {
		t.Fatalf("idempotent completion replay = %#v error=%v", completionReplay, err)
	}

	reviewInput := service.ReviewIssueCompletionInput{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ReportID: submitted.Report.ID,
		ActorType: "member", ActorID: parseUUID(testUserID), Verdict: "accepted",
		Results: []service.CompletionReviewResult{{CriterionIndex: 0, Accepted: true}, {CriterionIndex: 1, Accepted: true}},
	}
	if _, err = testHandler.IssueExecution.ReviewCompletion(ctx, reviewInput); !errors.Is(err, service.ErrIssueCompletionConflict) {
		t.Fatalf("review without canonical PR link error = %v, want conflict", err)
	}

	var pullRequestID string
	if err = testPool.QueryRow(ctx, `
		INSERT INTO github_pull_request (
		  workspace_id, installation_id, repo_owner, repo_name, pr_number,
		  title, state, html_url, pr_created_at, pr_updated_at
		) VALUES ($1, 65, 'example', 'game', 65, 'LRM-1621 restore visuals', 'merged',
		          'https://github.com/example/game/pull/65', now(), now())
		RETURNING id`, issue.WorkspaceID).Scan(&pullRequestID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM github_pull_request WHERE id=$1`, pullRequestID)
	})
	if _, err = testPool.Exec(ctx, `INSERT INTO issue_pull_request(issue_id,pull_request_id) VALUES($1,$2)`, issue.ID, pullRequestID); err != nil {
		t.Fatal(err)
	}
	accepted, err := testHandler.IssueExecution.ReviewCompletion(ctx, reviewInput)
	if err != nil {
		t.Fatalf("review with canonical PR link: %v", err)
	}
	if accepted.Issue.Status != "done" || accepted.Report.ReviewStatus != "accepted" || !accepted.Report.ReviewCommentID.Valid {
		t.Fatalf("accepted completion outcome = issue %s report %s comment=%v",
			accepted.Issue.Status, accepted.Report.ReviewStatus, accepted.Report.ReviewCommentID.Valid)
	}
	replayed, err := testHandler.IssueExecution.ReviewCompletion(ctx, reviewInput)
	if err != nil || !replayed.Replayed || replayed.Report.ID != accepted.Report.ID || replayed.Comment.ID != accepted.Comment.ID {
		t.Fatalf("idempotent accepted review = %#v error=%v", replayed, err)
	}
}

func TestRejectedCompletionCreatesSuccessorRunAndPreservesReport(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issue, agentID, run := prepareCompletionRun(t, "web game rejection")
	input := webGameCompletionInput(issue, agentID, uuidToString(run.RunID))
	// This fixture tests review lineage, not GitHub integration.
	input.AcceptanceResults[1].EvidenceRefs = []service.CompletionEvidenceRef{{Kind: "screenshot", Ref: "artifact://visual-gate"}}
	input.ArtifactRefs = nil
	submitted, err := testHandler.IssueExecution.SubmitCompletion(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := testHandler.IssueExecution.ReviewCompletion(ctx, service.ReviewIssueCompletionInput{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ReportID: submitted.Report.ID,
		ActorType: "member", ActorID: parseUUID(testUserID), Verdict: "rejected",
		Reason: "线上截图仍有森林接缝",
		Results: []service.CompletionReviewResult{
			{CriterionIndex: 0, Accepted: false, Reason: "森林接缝仍可见"},
			{CriterionIndex: 1, Accepted: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Issue.Status != "todo" || rejected.Report.ReviewStatus != "rejected" || !rejected.ExecutionOutcome.Dispatch {
		t.Fatalf("rejected outcome = status %s review %s dispatch %v",
			rejected.Issue.Status, rejected.Report.ReviewStatus, rejected.ExecutionOutcome.Dispatch)
	}
	claim, err := testHandler.Queries.GetActiveIssueExecution(ctx, db.GetActiveIssueExecutionParams{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.RunID == run.RunID || claim.IssueExecutionRevision != rejected.Issue.ExecutionRevision {
		t.Fatalf("successor claim = %#v", claim)
	}
	var payloadParent, outboxStatus string
	if err = testPool.QueryRow(ctx, `SELECT request_payload->>'parent_run_id', status FROM issue_dispatch_outbox WHERE run_id=$1`, claim.RunID).Scan(&payloadParent, &outboxStatus); err != nil {
		t.Fatal(err)
	}
	if payloadParent != uuidToString(run.RunID) {
		t.Fatalf("successor parent Run = %q, want %s", payloadParent, uuidToString(run.RunID))
	}
	if outboxStatus != "delivered" {
		t.Fatalf("rejected review successor outbox = %q, want delivered", outboxStatus)
	}
	var predecessorStatus string
	if err = testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id=$1`, run.RunID).Scan(&predecessorStatus); err != nil {
		t.Fatal(err)
	}
	if predecessorStatus != "suppressed" {
		t.Fatalf("rejected review predecessor status = %q, want suppressed", predecessorStatus)
	}
	successor, err := testHandler.Queries.GetAgentInboxEvent(ctx, claim.RunID)
	if err != nil || successor.ParentTaskID != run.RunID {
		t.Fatalf("recovered successor Run = %#v error=%v", successor, err)
	}
}

func TestCompletionAuthorCannotReviewOwnReport(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issue, agentID, run := prepareCompletionRun(t, "completion self review")
	input := webGameCompletionInput(issue, agentID, uuidToString(run.RunID))
	input.AcceptanceResults[1].EvidenceRefs = []service.CompletionEvidenceRef{{Kind: "test", Ref: "ci:green"}}
	input.ArtifactRefs = nil
	submitted, err := testHandler.IssueExecution.SubmitCompletion(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = testHandler.IssueExecution.ReviewCompletion(ctx, service.ReviewIssueCompletionInput{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ReportID: submitted.Report.ID,
		ActorType: "agent", ActorID: parseUUID(agentID), ActorRunID: run.RunID, Verdict: "accepted",
		Results: []service.CompletionReviewResult{{CriterionIndex: 0, Accepted: true}, {CriterionIndex: 1, Accepted: true}},
	})
	if !errors.Is(err, service.ErrIssueCompletionForbidden) {
		t.Fatalf("self-review error = %v, want forbidden", err)
	}
}

func TestIssueDeleteRetainsCanonicalRunHistoryAndCascadesCompletionReport(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issue, agentID, run := prepareCompletionRun(t, "completion deletion retention")
	input := webGameCompletionInput(issue, agentID, uuidToString(run.RunID))
	input.AcceptanceResults[1].EvidenceRefs = []service.CompletionEvidenceRef{{Kind: "test", Ref: "ci:green"}}
	input.ArtifactRefs = nil
	if _, err := testHandler.IssueExecution.SubmitCompletion(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM issue WHERE id=$1`, issue.ID); err != nil {
		t.Fatalf("delete Issue with canonical Run history: %v", err)
	}
	var reportCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM issue_completion_report WHERE issue_id=$1`, issue.ID).Scan(&reportCount); err != nil {
		t.Fatal(err)
	}
	if reportCount != 0 {
		t.Fatalf("completion reports after Issue delete = %d, want 0", reportCount)
	}
	retained, err := testHandler.Queries.GetAgentInboxEvent(ctx, run.RunID)
	if err != nil {
		t.Fatalf("retained canonical Run: %v", err)
	}
	if retained.IssueID.Valid || retained.IssueRunKind.String != "canonical" {
		t.Fatalf("retained Run Issue/canonical fields = %#v/%#v", retained.IssueID, retained.IssueRunKind)
	}
}

func TestCompletionReportIsSupersededWhenAcceptanceContractChanges(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issue, agentID, run := prepareCompletionRun(t, "completion contract superseded")
	input := webGameCompletionInput(issue, agentID, uuidToString(run.RunID))
	input.AcceptanceResults[1].EvidenceRefs = []service.CompletionEvidenceRef{{Kind: "test", Ref: "ci:green"}}
	input.ArtifactRefs = nil
	submitted, err := testHandler.IssueExecution.SubmitCompletion(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	current, err := testHandler.Queries.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	changedCriteria := []byte(`["旧世界与新世界地形连续且树木完整","必须提供真实线上独立视觉验收"]`)
	if _, err = testHandler.IssueExecution.UpdateIssue(ctx, db.UpdateIssueParams{
		ID: current.ID, AssigneeType: current.AssigneeType, AssigneeID: current.AssigneeID,
		StartDate: current.StartDate, DueDate: current.DueDate, ParentIssueID: current.ParentIssueID,
		ProjectID: current.ProjectID, AcceptanceCriteria: changedCriteria,
	}, current.WorkspaceID, service.IssueExecutionReconcileOptions{
		TriggerKind: "acceptance_contract_changed", Invalidate: true,
	}); err != nil {
		t.Fatal(err)
	}
	var reviewStatus string
	if err = testPool.QueryRow(ctx, `SELECT review_status FROM issue_completion_report WHERE id=$1`, submitted.Report.ID).Scan(&reviewStatus); err != nil {
		t.Fatal(err)
	}
	if reviewStatus != "superseded" {
		t.Fatalf("stale completion report status = %q, want superseded", reviewStatus)
	}
}
