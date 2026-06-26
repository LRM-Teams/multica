package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// forkReconstructableActions maps an overwritable issue field to the
// activity_log action whose details carry the field's pre-change value, and so
// let ForkIssueSubtree roll the field back to its value at the branch point.
//
// The source of truth for what the activity log records is
// cmd/server/activity_listeners.go: every entry here MUST correspond to an
// action emitted there whose details JSON includes a "from"/"from_*" value.
//
// `description` is deliberately absent: the activity log records a bare
// `description_updated` with empty `{}` details (no old/new text), so a forked
// issue's description cannot be reconstructed to the branch point and instead
// keeps the source's current value. Same for acceptance_criteria, context_refs,
// metadata, position, and parent_issue_id, which are not logged with values at
// all. This is a documented v1 limitation, not a bug — see the design doc §3.2.
var forkReconstructableActions = map[string]string{
	"status":     "status_changed",
	"priority":   "priority_changed",
	"title":      "title_changed",
	"due_date":   "due_date_changed",
	"start_date": "start_date_changed",
	"assignee":   "assignee_changed",
}

// IssueForkQueries is the subset of *db.Queries that IssueForkService needs.
// It is an interface so tests can inject a fake without a database; the
// concrete *db.Queries satisfies it directly.
type IssueForkQueries interface {
	GetIssue(ctx context.Context, id pgtype.UUID) (db.Issue, error)
	GetTaskMessageAtSeq(ctx context.Context, arg db.GetTaskMessageAtSeqParams) (db.TaskMessage, error)
	ListActivityLogForIssueAfter(ctx context.Context, arg db.ListActivityLogForIssueAfterParams) ([]db.ActivityLog, error)
	IncrementIssueCounter(ctx context.Context, id pgtype.UUID) (int32, error)
	CreateForkedIssue(ctx context.Context, arg db.CreateForkedIssueParams) (db.Issue, error)
}

// IssueForkService forks an issue at a past (task_id, seq) branch point,
// reconstructing overwritten issue fields from the activity log so the fork
// reflects the issue as it was when the source agent reached that seq.
type IssueForkService struct {
	Queries IssueForkQueries
}

// NewIssueForkService constructs an IssueForkService. In production the caller
// passes the handler's *db.Queries.
func NewIssueForkService(q IssueForkQueries) *IssueForkService {
	return &IssueForkService{Queries: q}
}

// ForkIssueSubtree forks sourceIssueID at the branch point (taskID, seq) and
// returns the new forked issue row.
//
// Overwritable issue fields (status, priority, title, due/start date, assignee)
// are reconstructed to their value at the branch point from the activity log;
// all other fields are copied from the source as-is. The forked row records its
// provenance via forked_from_issue_id / forked_at_seq / forked_at_task_id and
// gets a fresh workspace-scoped issue number.
//
// Copying the append-only subtree (comments, sub-issues, task_messages) cut at
// the branch point is deferred to a later phase: it requires creating a new
// agent task to host the copied transcript, which is out of scope here. This
// method forks the single issue entity with point-in-time field
// reconstruction, which is the novel, independently testable core.
func (s *IssueForkService) ForkIssueSubtree(
	ctx context.Context,
	sourceIssueID pgtype.UUID,
	taskID pgtype.UUID,
	seq int32,
) (db.Issue, error) {
	source, err := s.Queries.GetIssue(ctx, sourceIssueID)
	if err != nil {
		return db.Issue{}, fmt.Errorf("get source issue: %w", err)
	}

	// The task_message at the branch seq pins the branch point in time.
	branchMsg, err := s.Queries.GetTaskMessageAtSeq(ctx, db.GetTaskMessageAtSeqParams{
		TaskID: taskID,
		Seq:    seq,
	})
	if err != nil {
		return db.Issue{}, fmt.Errorf("get task_message at seq %d: %w", seq, err)
	}

	reconstructed, err := s.reconstructIssueAt(ctx, source, branchMsg.CreatedAt)
	if err != nil {
		return db.Issue{}, fmt.Errorf("reconstruct issue fields: %w", err)
	}

	number, err := s.Queries.IncrementIssueCounter(ctx, source.WorkspaceID)
	if err != nil {
		return db.Issue{}, fmt.Errorf("allocate issue number: %w", err)
	}

	forked, err := s.Queries.CreateForkedIssue(ctx, db.CreateForkedIssueParams{
		WorkspaceID:        reconstructed.WorkspaceID,
		Title:              reconstructed.Title,
		Description:        reconstructed.Description,
		Status:             reconstructed.Status,
		Priority:           reconstructed.Priority,
		AssigneeType:       reconstructed.AssigneeType,
		AssigneeID:         reconstructed.AssigneeID,
		CreatorType:        reconstructed.CreatorType,
		CreatorID:          reconstructed.CreatorID,
		ParentIssueID:      reconstructed.ParentIssueID,
		AcceptanceCriteria: reconstructed.AcceptanceCriteria,
		ContextRefs:        reconstructed.ContextRefs,
		Position:           reconstructed.Position,
		StartDate:          reconstructed.StartDate,
		DueDate:            reconstructed.DueDate,
		Metadata:           reconstructed.Metadata,
		Number:             number,
		ProjectID:          reconstructed.ProjectID,
		ForkedFromIssueID:  sourceIssueID,
		ForkedAtSeq:        pgtype.Int4{Int32: seq, Valid: true},
		ForkedAtTaskID:     taskID,
	})
	if err != nil {
		return db.Issue{}, fmt.Errorf("create forked issue: %w", err)
	}

	slog.Info("issue forked",
		"source_issue_id", util.UUIDToString(sourceIssueID),
		"forked_issue_id", util.UUIDToString(forked.ID),
		"task_id", util.UUIDToString(taskID),
		"seq", seq,
	)
	return forked, nil
}

// reconstructIssueAt returns a copy of source with its overwritable fields
// rolled back to the values they held at cutoff.
//
// Strategy (roll-back): the source row already reflects every change. For each
// tracked field, the earliest activity entry that occurred strictly AFTER the
// cutoff records — in its "from" value — what the field was immediately before
// that change, i.e. its value at the branch point. A field with no post-cutoff
// change is unchanged since the branch point, so the source value already is
// the branch-point value and we leave it untouched.
func (s *IssueForkService) reconstructIssueAt(
	ctx context.Context,
	source db.Issue,
	cutoff pgtype.Timestamptz,
) (db.Issue, error) {
	entries, err := s.Queries.ListActivityLogForIssueAfter(ctx, db.ListActivityLogForIssueAfterParams{
		IssueID: source.ID,
		Cutoff:  cutoff,
	})
	if err != nil {
		return db.Issue{}, fmt.Errorf("list activity log: %w", err)
	}

	out := source
	applied := make(map[string]bool, len(forkReconstructableActions))
	for _, e := range entries {
		// Only the earliest post-cutoff change per action carries the
		// branch-point value; entries are ASC, so skip later ones.
		if applied[e.Action] {
			continue
		}
		ok, err := applyRollback(&out, e)
		if err != nil {
			slog.Warn("issue fork: skipping malformed activity entry",
				"issue_id", util.UUIDToString(source.ID),
				"entry_id", util.UUIDToString(e.ID),
				"action", e.Action,
				"error", err)
			continue
		}
		if ok {
			applied[e.Action] = true
		}
	}
	return out, nil
}

// applyRollback rolls a single issue field back using one activity entry's
// "from" value. It returns ok=true when the action was recognized and applied,
// false when the action is not one we reconstruct (so the caller can ignore
// it). All recorded values are JSON strings (see activity_listeners.go).
func applyRollback(issue *db.Issue, e db.ActivityLog) (bool, error) {
	var d map[string]string
	if err := json.Unmarshal(e.Details, &d); err != nil {
		return false, fmt.Errorf("unmarshal details: %w", err)
	}

	switch e.Action {
	case "status_changed":
		issue.Status = d["from"]
	case "priority_changed":
		issue.Priority = d["from"]
	case "title_changed":
		issue.Title = d["from"]
	case "due_date_changed":
		issue.DueDate = parseCalendarDateOrNull(d["from"])
	case "start_date_changed":
		issue.StartDate = parseCalendarDateOrNull(d["from"])
	case "assignee_changed":
		applyAssigneeRollback(issue, d["from_type"], d["from_id"])
	default:
		return false, nil
	}
	return true, nil
}

// applyAssigneeRollback restores assignee_type / assignee_id from an
// assignee_changed entry's "from_type" / "from_id". An empty from_type means
// the issue was unassigned at the branch point.
func applyAssigneeRollback(issue *db.Issue, fromType, fromID string) {
	if fromType == "" {
		issue.AssigneeType = pgtype.Text{}
		issue.AssigneeID = pgtype.UUID{}
		return
	}
	issue.AssigneeType = pgtype.Text{String: fromType, Valid: true}
	if fromID == "" {
		issue.AssigneeID = pgtype.UUID{}
		return
	}
	if u, err := util.ParseUUID(fromID); err == nil {
		issue.AssigneeID = u
	} else {
		issue.AssigneeID = pgtype.UUID{}
	}
}

// parseCalendarDateOrNull parses a "YYYY-MM-DD" date, returning a NULL date for
// an empty string or unparseable value (a previously-unset due/start date).
func parseCalendarDateOrNull(s string) pgtype.Date {
	if s == "" {
		return pgtype.Date{}
	}
	d, err := util.ParseCalendarDate(s)
	if err != nil {
		return pgtype.Date{}
	}
	return d
}
