package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrIssueCompletionConflict   = errors.New("issue completion conflict")
	ErrIssueCompletionForbidden  = errors.New("issue completion forbidden")
	ErrIssueCompletionValidation = errors.New("invalid issue completion")
)

type CompletionEvidenceRef struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

type CompletionAcceptanceResult struct {
	CriterionIndex int                     `json:"criterion_index"`
	Criterion      string                  `json:"criterion"`
	Satisfied      bool                    `json:"satisfied"`
	EvidenceRefs   []CompletionEvidenceRef `json:"evidence_refs"`
}

type CompletionReviewResult struct {
	CriterionIndex int    `json:"criterion_index"`
	Accepted       bool   `json:"accepted"`
	Reason         string `json:"reason,omitempty"`
}

type IssueCompletionReport struct {
	ID                     pgtype.UUID        `json:"id"`
	WorkspaceID            pgtype.UUID        `json:"workspace_id"`
	IssueID                pgtype.UUID        `json:"issue_id"`
	RunID                  pgtype.UUID        `json:"run_id"`
	IssueExecutionRevision int64              `json:"issue_execution_revision"`
	SubmittedByAgentID     pgtype.UUID        `json:"submitted_by_agent_id"`
	Summary                string             `json:"summary"`
	AcceptanceResults      []byte             `json:"acceptance_results"`
	ArtifactRefs           []byte             `json:"artifact_refs"`
	Risks                  []byte             `json:"risks"`
	RequestHash            string             `json:"request_hash"`
	VisibleCommentID       pgtype.UUID        `json:"visible_comment_id"`
	ReviewStatus           string             `json:"review_status"`
	ReviewerType           pgtype.Text        `json:"reviewer_type"`
	ReviewerID             pgtype.UUID        `json:"reviewer_id"`
	ReviewReason           pgtype.Text        `json:"review_reason"`
	ReviewResults          []byte             `json:"review_results"`
	ReviewCommentID        pgtype.UUID        `json:"review_comment_id"`
	ReviewedAt             pgtype.Timestamptz `json:"reviewed_at"`
	CreatedAt              pgtype.Timestamptz `json:"created_at"`
	UpdatedAt              pgtype.Timestamptz `json:"updated_at"`
}

type SubmitIssueCompletionInput struct {
	WorkspaceID               pgtype.UUID
	IssueID                   pgtype.UUID
	RunID                     pgtype.UUID
	AgentID                   pgtype.UUID
	ExpectedExecutionRevision int64
	Summary                   string
	AcceptanceResults         []CompletionAcceptanceResult
	ArtifactRefs              []CompletionEvidenceRef
	Risks                     []string
}

type ReviewIssueCompletionInput struct {
	WorkspaceID pgtype.UUID
	IssueID     pgtype.UUID
	ReportID    pgtype.UUID
	ActorType   string
	ActorID     pgtype.UUID
	ActorRunID  pgtype.UUID
	Verdict     string
	Reason      string
	Results     []CompletionReviewResult
}

type IssueCompletionOutcome struct {
	Report           IssueCompletionReport
	Issue            db.Issue
	Comment          db.Comment
	ExecutionOutcome IssueExecutionReconcileOutcome
	// Replayed means the same actor retried an already committed request.
	// Callers must not emit duplicate realtime events for this outcome.
	Replayed bool
}

const issueCompletionReportColumns = `
id, workspace_id, issue_id, run_id, issue_execution_revision,
submitted_by_agent_id, summary, acceptance_results, artifact_refs, risks,
request_hash, visible_comment_id, review_status, reviewer_type, reviewer_id,
review_reason, review_results, review_comment_id, reviewed_at, created_at, updated_at`

func scanIssueCompletionReport(row pgx.Row) (IssueCompletionReport, error) {
	var report IssueCompletionReport
	err := row.Scan(
		&report.ID, &report.WorkspaceID, &report.IssueID, &report.RunID,
		&report.IssueExecutionRevision, &report.SubmittedByAgentID, &report.Summary,
		&report.AcceptanceResults, &report.ArtifactRefs, &report.Risks, &report.RequestHash,
		&report.VisibleCommentID, &report.ReviewStatus, &report.ReviewerType,
		&report.ReviewerID, &report.ReviewReason, &report.ReviewResults,
		&report.ReviewCommentID, &report.ReviewedAt, &report.CreatedAt, &report.UpdatedAt,
	)
	return report, err
}

func normalizeEvidenceRefs(refs []CompletionEvidenceRef) ([]CompletionEvidenceRef, error) {
	out := make([]CompletionEvidenceRef, len(refs))
	for i, ref := range refs {
		ref.Kind = strings.TrimSpace(ref.Kind)
		ref.Ref = strings.TrimSpace(ref.Ref)
		if ref.Kind == "" || len(ref.Kind) > 40 || ref.Ref == "" || len(ref.Ref) > 2000 {
			return nil, fmt.Errorf("%w: evidence_refs[%d] requires bounded kind and ref", ErrIssueCompletionValidation, i)
		}
		out[i] = ref
	}
	return out, nil
}

func normalizeCompletionPayload(input *SubmitIssueCompletionInput) error {
	input.Summary = strings.TrimSpace(input.Summary)
	if input.Summary == "" || len(input.Summary) > 8000 {
		return fmt.Errorf("%w: summary is required and must not exceed 8000 bytes", ErrIssueCompletionValidation)
	}
	for i := range input.AcceptanceResults {
		refs, err := normalizeEvidenceRefs(input.AcceptanceResults[i].EvidenceRefs)
		if err != nil {
			return err
		}
		input.AcceptanceResults[i].EvidenceRefs = refs
	}
	refs, err := normalizeEvidenceRefs(input.ArtifactRefs)
	if err != nil {
		return err
	}
	input.ArtifactRefs = refs
	if len(input.Risks) > 50 {
		return fmt.Errorf("%w: at most 50 risks are allowed", ErrIssueCompletionValidation)
	}
	for i := range input.Risks {
		input.Risks[i] = strings.TrimSpace(input.Risks[i])
		if input.Risks[i] == "" || len(input.Risks[i]) > 2000 {
			return fmt.Errorf("%w: risks[%d] is empty or too long", ErrIssueCompletionValidation, i)
		}
	}
	return nil
}

func validateCompletionPayload(issue db.Issue, input *SubmitIssueCompletionInput) error {
	if err := normalizeCompletionPayload(input); err != nil {
		return err
	}
	var criteria []string
	if err := json.Unmarshal(issue.AcceptanceCriteria, &criteria); err != nil || criteria == nil {
		return fmt.Errorf("%w: issue acceptance criteria are malformed", ErrIssueCompletionValidation)
	}
	if len(criteria) == 0 {
		return fmt.Errorf("%w: issue has no acceptance criteria", ErrIssueCompletionValidation)
	}
	if len(input.AcceptanceResults) != len(criteria) {
		return fmt.Errorf("%w: exactly one result is required for each acceptance criterion", ErrIssueCompletionValidation)
	}
	seen := make(map[int]bool, len(criteria))
	for i := range input.AcceptanceResults {
		result := &input.AcceptanceResults[i]
		if result.CriterionIndex < 0 || result.CriterionIndex >= len(criteria) || seen[result.CriterionIndex] {
			return fmt.Errorf("%w: criterion_index must be unique and in range", ErrIssueCompletionValidation)
		}
		seen[result.CriterionIndex] = true
		if result.Criterion != criteria[result.CriterionIndex] {
			return fmt.Errorf("%w: criterion %d no longer matches the Issue contract", ErrIssueCompletionConflict, result.CriterionIndex)
		}
		if !result.Satisfied {
			return fmt.Errorf("%w: criterion %d is not satisfied", ErrIssueCompletionValidation, result.CriterionIndex)
		}
		if len(result.EvidenceRefs) == 0 {
			return fmt.Errorf("%w: criterion %d requires evidence", ErrIssueCompletionValidation, result.CriterionIndex)
		}
	}
	return nil
}

func completionRequestHash(input SubmitIssueCompletionInput) (string, []byte, []byte, []byte, error) {
	results, err := json.Marshal(input.AcceptanceResults)
	if err != nil {
		return "", nil, nil, nil, err
	}
	artifacts, err := json.Marshal(input.ArtifactRefs)
	if err != nil {
		return "", nil, nil, nil, err
	}
	risks, err := json.Marshal(input.Risks)
	if err != nil {
		return "", nil, nil, nil, err
	}
	canonical, err := json.Marshal(struct {
		Revision  int64                        `json:"revision"`
		Summary   string                       `json:"summary"`
		Results   []CompletionAcceptanceResult `json:"results"`
		Artifacts []CompletionEvidenceRef      `json:"artifacts"`
		Risks     []string                     `json:"risks"`
	}{input.ExpectedExecutionRevision, input.Summary, input.AcceptanceResults, input.ArtifactRefs, input.Risks})
	if err != nil {
		return "", nil, nil, nil, err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), results, artifacts, risks, nil
}

func completionComment(input SubmitIssueCompletionInput) string {
	var b strings.Builder
	b.WriteString("Completion report\n\n")
	b.WriteString(input.Summary)
	b.WriteString("\n\nAcceptance evidence:\n")
	for _, result := range input.AcceptanceResults {
		fmt.Fprintf(&b, "- [x] %s\n", result.Criterion)
		for _, ref := range result.EvidenceRefs {
			fmt.Fprintf(&b, "  - %s: %s\n", ref.Kind, ref.Ref)
		}
	}
	if len(input.ArtifactRefs) > 0 {
		b.WriteString("\nArtifacts:\n")
		for _, ref := range input.ArtifactRefs {
			fmt.Fprintf(&b, "- %s: %s\n", ref.Kind, ref.Ref)
		}
	}
	if len(input.Risks) > 0 {
		b.WriteString("\nRisks / follow-ups:\n")
		for _, risk := range input.Risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}
	return strings.TrimSpace(b.String())
}

func (s *IssueExecutionService) SubmitCompletion(ctx context.Context, input SubmitIssueCompletionInput) (IssueCompletionOutcome, error) {
	if !input.WorkspaceID.Valid || !input.IssueID.Valid || !input.RunID.Valid || !input.AgentID.Valid {
		return IssueCompletionOutcome{}, fmt.Errorf("%w: missing identity", ErrIssueCompletionValidation)
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if _, err = q.GetIssueExecutionStateForUpdate(ctx, db.GetIssueExecutionStateForUpdateParams{
		IssueID: input.IssueID, WorkspaceID: input.WorkspaceID,
	}); err != nil {
		return IssueCompletionOutcome{}, err
	}
	issue, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: input.IssueID, WorkspaceID: input.WorkspaceID})
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	run, err := q.GetAgentInboxEvent(ctx, input.RunID)
	if err != nil || run.WorkspaceID != input.WorkspaceID || run.AgentID != input.AgentID ||
		!run.IssueID.Valid || run.IssueID != input.IssueID || run.Reason != "issue" ||
		run.TriggerCommentID.Valid || run.IssueRunKind.String != "canonical" {
		return IssueCompletionOutcome{}, ErrIssueCompletionForbidden
	}

	existing, existingErr := scanIssueCompletionReport(tx.QueryRow(ctx,
		`SELECT `+issueCompletionReportColumns+` FROM issue_completion_report
		 WHERE workspace_id=$1 AND issue_id=$2 AND run_id=$3`, input.WorkspaceID, input.IssueID, input.RunID))
	if existingErr == nil {
		if err = normalizeCompletionPayload(&input); err != nil {
			return IssueCompletionOutcome{}, err
		}
		hash, _, _, _, hashErr := completionRequestHash(input)
		if hashErr != nil {
			return IssueCompletionOutcome{}, hashErr
		}
		if existing.RequestHash != hash {
			return IssueCompletionOutcome{}, fmt.Errorf("%w: this Run already submitted a different report", ErrIssueCompletionConflict)
		}
		comment, commentErr := q.GetComment(ctx, existing.VisibleCommentID)
		if commentErr != nil {
			return IssueCompletionOutcome{}, commentErr
		}
		if err = tx.Commit(ctx); err != nil {
			return IssueCompletionOutcome{}, err
		}
		return IssueCompletionOutcome{Report: existing, Issue: issue, Comment: comment, Replayed: true}, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return IssueCompletionOutcome{}, existingErr
	}
	if issue.Status != "todo" && issue.Status != "in_progress" {
		return IssueCompletionOutcome{}, fmt.Errorf("%w: Issue must be todo or in_progress", ErrIssueCompletionConflict)
	}
	if issue.AssigneeType.String != "agent" || issue.AssigneeID != input.AgentID ||
		issue.ExecutionRevision != input.ExpectedExecutionRevision ||
		!run.IssueExecutionRevision.Valid || run.IssueExecutionRevision.Int64 != input.ExpectedExecutionRevision ||
		(run.Status != "draining" && run.Status != "running") {
		return IssueCompletionOutcome{}, fmt.Errorf("%w: Run, assignee, status, or execution revision is stale", ErrIssueCompletionConflict)
	}
	claim, err := q.GetActiveIssueExecution(ctx, db.GetActiveIssueExecutionParams{WorkspaceID: input.WorkspaceID, IssueID: input.IssueID})
	if err != nil || claim.RunID != input.RunID || claim.AgentID != input.AgentID ||
		claim.IssueExecutionRevision != input.ExpectedExecutionRevision {
		return IssueCompletionOutcome{}, fmt.Errorf("%w: active Issue claim does not match this Run", ErrIssueCompletionConflict)
	}
	if err = validateCompletionPayload(issue, &input); err != nil {
		return IssueCompletionOutcome{}, err
	}
	hash, results, artifacts, risks, err := completionRequestHash(input)
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	reportID := newPGUUID()
	report, err := scanIssueCompletionReport(tx.QueryRow(ctx, `
		INSERT INTO issue_completion_report (
		  id, workspace_id, issue_id, run_id, issue_execution_revision,
		  submitted_by_agent_id, summary, acceptance_results, artifact_refs, risks, request_hash
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING `+issueCompletionReportColumns,
		reportID, input.WorkspaceID, input.IssueID, input.RunID, input.ExpectedExecutionRevision,
		input.AgentID, input.Summary, results, artifacts, risks, hash))
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	comment, err := q.CreateComment(ctx, db.CreateCommentParams{
		IssueID: input.IssueID, WorkspaceID: input.WorkspaceID, AuthorType: "agent", AuthorID: input.AgentID,
		Content: completionComment(input), Type: "comment",
	})
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE issue_completion_report SET visible_comment_id=$2, updated_at=now() WHERE id=$1`, report.ID, comment.ID); err != nil {
		return IssueCompletionOutcome{}, err
	}
	report.VisibleCommentID = comment.ID
	updated, err := q.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID, Status: "in_review"})
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	outcome, err := s.ReconcileTx(ctx, tx, updated, IssueExecutionReconcileOptions{
		TriggerKind: "completion_submitted", Invalidate: true, KeepTerminalRunID: input.RunID,
		KeepCompletionReportRunID: input.RunID,
	})
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	updated, err = q.GetIssue(ctx, issue.ID)
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return IssueCompletionOutcome{}, err
	}
	s.PublishOutcome(ctx, outcome)
	return IssueCompletionOutcome{Report: report, Issue: updated, Comment: comment, ExecutionOutcome: outcome}, nil
}

func validateReviewResults(criteria []string, verdict string, results []CompletionReviewResult) error {
	if len(results) != len(criteria) {
		return fmt.Errorf("%w: review requires one verdict per acceptance criterion", ErrIssueCompletionValidation)
	}
	seen := make(map[int]bool, len(criteria))
	anyRejected := false
	for i := range results {
		result := &results[i]
		result.Reason = strings.TrimSpace(result.Reason)
		if result.CriterionIndex < 0 || result.CriterionIndex >= len(criteria) || seen[result.CriterionIndex] {
			return fmt.Errorf("%w: review criterion_index must be unique and in range", ErrIssueCompletionValidation)
		}
		seen[result.CriterionIndex] = true
		if !result.Accepted {
			anyRejected = true
			if result.Reason == "" {
				return fmt.Errorf("%w: rejected criterion %d requires a reason", ErrIssueCompletionValidation, result.CriterionIndex)
			}
		}
	}
	if verdict == "accepted" && anyRejected {
		return fmt.Errorf("%w: accepted review contains a rejected criterion", ErrIssueCompletionValidation)
	}
	if verdict == "rejected" && !anyRejected {
		return fmt.Errorf("%w: rejected review must reject at least one criterion", ErrIssueCompletionValidation)
	}
	return nil
}

func reviewComment(verdict, reason string, criteria []string, results []CompletionReviewResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Completion review: %s\n\n", verdict)
	for _, result := range results {
		mark := "x"
		if !result.Accepted {
			mark = " "
		}
		fmt.Fprintf(&b, "- [%s] %s", mark, criteria[result.CriterionIndex])
		if result.Reason != "" {
			fmt.Fprintf(&b, " — %s", result.Reason)
		}
		b.WriteByte('\n')
	}
	if reason != "" {
		fmt.Fprintf(&b, "\nReview note: %s", reason)
	}
	return strings.TrimSpace(b.String())
}

func ensureCanonicalPullRequestEvidence(ctx context.Context, tx pgx.Tx, report IssueCompletionReport) error {
	var results []CompletionAcceptanceResult
	var artifacts []CompletionEvidenceRef
	if err := json.Unmarshal(report.AcceptanceResults, &results); err != nil {
		return err
	}
	if err := json.Unmarshal(report.ArtifactRefs, &artifacts); err != nil {
		return err
	}
	refs := make(map[string]struct{})
	for _, result := range results {
		for _, ref := range result.EvidenceRefs {
			if strings.EqualFold(strings.TrimSpace(ref.Kind), "pull_request") {
				refs[strings.TrimRight(strings.TrimSpace(ref.Ref), "/")] = struct{}{}
			}
		}
	}
	for _, ref := range artifacts {
		if strings.EqualFold(strings.TrimSpace(ref.Kind), "pull_request") {
			refs[strings.TrimRight(strings.TrimSpace(ref.Ref), "/")] = struct{}{}
		}
	}
	for ref := range refs {
		var linked bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM issue_pull_request link
			  JOIN github_pull_request pull_request ON pull_request.id=link.pull_request_id
			  WHERE link.issue_id=$1 AND pull_request.workspace_id=$2
			    AND lower(rtrim(pull_request.html_url, '/'))=lower($3)
			)`, report.IssueID, report.WorkspaceID, ref).Scan(&linked); err != nil {
			return err
		}
		if !linked {
			return fmt.Errorf("%w: pull_request evidence %q is not present in the canonical Issue PR link table", ErrIssueCompletionConflict, ref)
		}
	}
	return nil
}

func equalJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftCanonical, leftErr := json.Marshal(leftValue)
	rightCanonical, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && string(leftCanonical) == string(rightCanonical)
}

func (s *IssueExecutionService) ReviewCompletion(ctx context.Context, input ReviewIssueCompletionInput) (IssueCompletionOutcome, error) {
	input.Verdict = strings.TrimSpace(input.Verdict)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Verdict != "accepted" && input.Verdict != "rejected" {
		return IssueCompletionOutcome{}, fmt.Errorf("%w: verdict must be accepted or rejected", ErrIssueCompletionValidation)
	}
	if input.Verdict == "rejected" && input.Reason == "" {
		return IssueCompletionOutcome{}, fmt.Errorf("%w: rejected review requires a reason", ErrIssueCompletionValidation)
	}
	if input.ActorType != "member" && input.ActorType != "agent" {
		return IssueCompletionOutcome{}, ErrIssueCompletionForbidden
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if _, err = q.GetIssueExecutionStateForUpdate(ctx, db.GetIssueExecutionStateForUpdateParams{IssueID: input.IssueID, WorkspaceID: input.WorkspaceID}); err != nil {
		return IssueCompletionOutcome{}, err
	}
	issue, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: input.IssueID, WorkspaceID: input.WorkspaceID})
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	report, err := scanIssueCompletionReport(tx.QueryRow(ctx,
		`SELECT `+issueCompletionReportColumns+` FROM issue_completion_report WHERE id=$1 AND workspace_id=$2 AND issue_id=$3`,
		input.ReportID, input.WorkspaceID, input.IssueID))
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	var criteria []string
	if err = json.Unmarshal(issue.AcceptanceCriteria, &criteria); err != nil || criteria == nil {
		return IssueCompletionOutcome{}, fmt.Errorf("%w: issue acceptance criteria are malformed", ErrIssueCompletionValidation)
	}
	if err = validateReviewResults(criteria, input.Verdict, input.Results); err != nil {
		return IssueCompletionOutcome{}, err
	}
	reviewResults, err := json.Marshal(input.Results)
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	if report.ReviewStatus != "pending" || issue.Status != "in_review" {
		if report.ReviewStatus == input.Verdict && report.ReviewerType.String == input.ActorType &&
			report.ReviewerID == input.ActorID && report.ReviewReason.String == input.Reason &&
			equalJSON(report.ReviewResults, reviewResults) && report.ReviewCommentID.Valid {
			comment, commentErr := q.GetComment(ctx, report.ReviewCommentID)
			if commentErr != nil {
				return IssueCompletionOutcome{}, commentErr
			}
			if err = tx.Commit(ctx); err != nil {
				return IssueCompletionOutcome{}, err
			}
			return IssueCompletionOutcome{Report: report, Issue: issue, Comment: comment, Replayed: true}, nil
		}
		return IssueCompletionOutcome{}, fmt.Errorf("%w: report is no longer pending review", ErrIssueCompletionConflict)
	}
	if input.Verdict == "accepted" {
		if err = ensureCanonicalPullRequestEvidence(ctx, tx, report); err != nil {
			return IssueCompletionOutcome{}, err
		}
	}
	if input.ActorType == "agent" {
		if input.ActorID == report.SubmittedByAgentID || !input.ActorRunID.Valid {
			return IssueCompletionOutcome{}, ErrIssueCompletionForbidden
		}
		reviewerRun, runErr := q.GetAgentInboxEvent(ctx, input.ActorRunID)
		if runErr != nil || reviewerRun.WorkspaceID != input.WorkspaceID || reviewerRun.AgentID != input.ActorID ||
			!reviewerRun.IssueID.Valid || reviewerRun.IssueID != input.IssueID ||
			(reviewerRun.Status != "draining" && reviewerRun.Status != "running") {
			return IssueCompletionOutcome{}, ErrIssueCompletionForbidden
		}
	}
	comment, err := q.CreateComment(ctx, db.CreateCommentParams{
		IssueID: input.IssueID, WorkspaceID: input.WorkspaceID, AuthorType: input.ActorType, AuthorID: input.ActorID,
		Content: reviewComment(input.Verdict, input.Reason, criteria, input.Results), Type: "comment",
	})
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	report, err = scanIssueCompletionReport(tx.QueryRow(ctx, `
		UPDATE issue_completion_report
		SET review_status=$2, reviewer_type=$3, reviewer_id=$4, review_reason=$5,
		    review_results=$6, review_comment_id=$7, reviewed_at=now(), updated_at=now()
		WHERE id=$1 AND review_status='pending'
		RETURNING `+issueCompletionReportColumns,
		report.ID, input.Verdict, input.ActorType, input.ActorID, input.Reason, reviewResults, comment.ID))
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	nextStatus := "done"
	if input.Verdict == "rejected" {
		nextStatus = "todo"
	}
	updated, err := q.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID, Status: nextStatus})
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	reconcile := IssueExecutionReconcileOptions{TriggerKind: "completion_" + input.Verdict}
	if input.Verdict == "rejected" {
		reconcile.Invalidate = true
		reconcile.ParentRunID = report.RunID
	}
	outcome, err := s.ReconcileTx(ctx, tx, updated, reconcile)
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	updated, err = q.GetIssue(ctx, issue.ID)
	if err != nil {
		return IssueCompletionOutcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return IssueCompletionOutcome{}, err
	}
	s.PublishOutcome(ctx, outcome)
	return IssueCompletionOutcome{Report: report, Issue: updated, Comment: comment, ExecutionOutcome: outcome}, nil
}

func (s *IssueExecutionService) ListCompletionReports(ctx context.Context, workspaceID, issueID pgtype.UUID) ([]IssueCompletionReport, error) {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT `+issueCompletionReportColumns+` FROM issue_completion_report
		WHERE workspace_id=$1 AND issue_id=$2 ORDER BY created_at DESC, id DESC`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reports := []IssueCompletionReport{}
	for rows.Next() {
		report, scanErr := scanIssueCompletionReport(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		reports = append(reports, report)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return reports, nil
}
