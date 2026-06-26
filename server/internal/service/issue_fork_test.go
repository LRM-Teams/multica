package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeForkQueries is an in-memory IssueForkQueries used to exercise
// ForkIssueSubtree without a database. ListActivityLogForIssueAfter applies the
// same created_at > cutoff filter the real SQL query does, so reconstruction is
// tested against the real contract.
type fakeForkQueries struct {
	source       db.Issue
	branchMsg    db.TaskMessage
	activities   []db.ActivityLog
	nextNumber   int32
	createCalls  int
	lastCreate   db.CreateForkedIssueParams
	getIssueErr  error
	branchMsgErr error
}

func (f *fakeForkQueries) GetIssue(_ context.Context, id pgtype.UUID) (db.Issue, error) {
	if f.getIssueErr != nil {
		return db.Issue{}, f.getIssueErr
	}
	if id == f.source.ID {
		return f.source, nil
	}
	return db.Issue{}, pgx.ErrNoRows
}

func (f *fakeForkQueries) GetTaskMessageAtSeq(_ context.Context, arg db.GetTaskMessageAtSeqParams) (db.TaskMessage, error) {
	if f.branchMsgErr != nil {
		return db.TaskMessage{}, f.branchMsgErr
	}
	if arg.TaskID == f.branchMsg.TaskID && arg.Seq == f.branchMsg.Seq {
		return f.branchMsg, nil
	}
	return db.TaskMessage{}, pgx.ErrNoRows
}

func (f *fakeForkQueries) ListActivityLogForIssueAfter(_ context.Context, arg db.ListActivityLogForIssueAfterParams) ([]db.ActivityLog, error) {
	out := []db.ActivityLog{}
	for _, e := range f.activities {
		if e.IssueID == arg.IssueID && e.CreatedAt.Time.After(arg.Cutoff.Time) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeForkQueries) IncrementIssueCounter(_ context.Context, _ pgtype.UUID) (int32, error) {
	f.nextNumber++
	return f.nextNumber, nil
}

func (f *fakeForkQueries) CreateForkedIssue(_ context.Context, arg db.CreateForkedIssueParams) (db.Issue, error) {
	f.createCalls++
	f.lastCreate = arg
	// Echo the params back as the created row, mirroring RETURNING *.
	return db.Issue{
		ID:                pgtype.UUID{Bytes: [16]byte{0xF0, byte(f.createCalls)}, Valid: true},
		WorkspaceID:       arg.WorkspaceID,
		Title:             arg.Title,
		Description:       arg.Description,
		Status:            arg.Status,
		Priority:          arg.Priority,
		AssigneeType:      arg.AssigneeType,
		AssigneeID:        arg.AssigneeID,
		DueDate:           arg.DueDate,
		StartDate:         arg.StartDate,
		Number:            arg.Number,
		ForkedFromIssueID: arg.ForkedFromIssueID,
		ForkedAtSeq:       arg.ForkedAtSeq,
		ForkedAtTaskID:    arg.ForkedAtTaskID,
	}, nil
}

// --- helpers --------------------------------------------------------------

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func uuidByte(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

// activity builds an activity_log entry matching the real shape written by
// cmd/server/activity_listeners.go.
func activity(issueID pgtype.UUID, action, details string, at time.Time) db.ActivityLog {
	return db.ActivityLog{
		ID:        uuidByte(0xA0),
		IssueID:   issueID,
		Action:    action,
		Details:   []byte(details),
		CreatedAt: ts(at),
	}
}

// --- coverage contract ----------------------------------------------------

// TestForkReconstructableActionsAreHandled is a build-time contract: every
// field listed in forkReconstructableActions must be rolled back by
// applyRollback. If someone adds a field to the map without teaching
// applyRollback its action, this fails.
func TestForkReconstructableActionsAreHandled(t *testing.T) {
	for field, action := range forkReconstructableActions {
		issue := &db.Issue{}
		// Minimal valid details for each action shape.
		details := `{"from":"x"}`
		if action == "assignee_changed" {
			details = `{"from_type":"member","from_id":""}`
		}
		ok, err := applyRollback(issue, db.ActivityLog{Action: action, Details: []byte(details)})
		if err != nil {
			t.Errorf("field %q action %q: applyRollback error: %v", field, action, err)
		}
		if !ok {
			t.Errorf("field %q action %q is in forkReconstructableActions but applyRollback does not handle it", field, action)
		}
	}
}

// TestDescriptionIsNotReconstructable documents the activity-log limitation:
// description_updated carries no value, so description must NOT be in the
// reconstructable set (it falls back to the source's current value).
func TestDescriptionIsNotReconstructable(t *testing.T) {
	if _, ok := forkReconstructableActions["description"]; ok {
		t.Fatal("description must not be reconstructable: the activity log records " +
			"description_updated with empty details, so there is no value to roll back to")
	}
	ok, _ := applyRollback(&db.Issue{}, db.ActivityLog{Action: "description_updated", Details: []byte("{}")})
	if ok {
		t.Fatal("applyRollback should not claim to handle description_updated")
	}
}

// --- ForkIssueSubtree -----------------------------------------------------

func newForkFixture() (*fakeForkQueries, pgtype.UUID, pgtype.UUID, time.Time) {
	sourceID := uuidByte(1)
	taskID := uuidByte(2)
	cutoff := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f := &fakeForkQueries{
		source: db.Issue{
			ID:          sourceID,
			WorkspaceID: uuidByte(9),
			Title:       "final title",
			Status:      "done",
			Priority:    "urgent",
			CreatorType: "agent",
			CreatorID:   uuidByte(3),
		},
		branchMsg: db.TaskMessage{TaskID: taskID, Seq: 5, CreatedAt: ts(cutoff)},
	}
	return f, sourceID, taskID, cutoff
}

func TestForkIssueSubtree_ReconstructsStatusFromActivityLog(t *testing.T) {
	f, sourceID, taskID, cutoff := newForkFixture()
	// A pre-cutoff change (must be ignored — filtered out by the query) and the
	// earliest post-cutoff change, whose "from" is the value at the branch point.
	f.activities = []db.ActivityLog{
		activity(sourceID, "status_changed", `{"from":"backlog","to":"in_progress"}`, cutoff.Add(-2*time.Hour)),
		activity(sourceID, "status_changed", `{"from":"in_progress","to":"done"}`, cutoff.Add(30*time.Minute)),
	}

	forked, err := NewIssueForkService(f).ForkIssueSubtree(context.Background(), sourceID, taskID, 5)
	if err != nil {
		t.Fatalf("ForkIssueSubtree: %v", err)
	}
	if forked.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", forked.Status)
	}
	if !forked.ForkedFromIssueID.Valid || forked.ForkedFromIssueID != sourceID {
		t.Errorf("forked_from_issue_id = %+v, want %+v", forked.ForkedFromIssueID, sourceID)
	}
	if !forked.ForkedAtSeq.Valid || forked.ForkedAtSeq.Int32 != 5 {
		t.Errorf("forked_at_seq = %+v, want 5", forked.ForkedAtSeq)
	}
	if forked.ForkedAtTaskID != taskID {
		t.Errorf("forked_at_task_id = %+v, want %+v", forked.ForkedAtTaskID, taskID)
	}
	if forked.Number != 1 {
		t.Errorf("number = %d, want 1 (allocated via counter)", forked.Number)
	}
}

func TestForkIssueSubtree_NoChangesKeepsCurrentValues(t *testing.T) {
	f, sourceID, taskID, _ := newForkFixture()
	// No activity after the branch point: the fork mirrors the source.
	forked, err := NewIssueForkService(f).ForkIssueSubtree(context.Background(), sourceID, taskID, 5)
	if err != nil {
		t.Fatalf("ForkIssueSubtree: %v", err)
	}
	if forked.Status != "done" || forked.Priority != "urgent" || forked.Title != "final title" {
		t.Errorf("expected source values preserved, got status=%q priority=%q title=%q",
			forked.Status, forked.Priority, forked.Title)
	}
}

func TestForkIssueSubtree_PropagatesGetIssueError(t *testing.T) {
	f, sourceID, taskID, _ := newForkFixture()
	f.getIssueErr = pgx.ErrNoRows
	if _, err := NewIssueForkService(f).ForkIssueSubtree(context.Background(), sourceID, taskID, 5); err == nil {
		t.Fatal("expected error when source issue is missing")
	}
}

// TestForkIssueSubtree_ReconstructsEachField is table-driven over every
// reconstructable field. Each case has the field changed AFTER the branch
// point; reconstruction must yield the pre-change ("from") value, not the
// source's current value.
func TestForkIssueSubtree_ReconstructsEachField(t *testing.T) {
	cases := []struct {
		name    string
		action  string
		details string
		mutate  func(*db.Issue) // set the source's current (post-branch) value
		assert  func(*testing.T, db.Issue)
	}{
		{
			name:    "title",
			action:  "title_changed",
			details: `{"from":"title at branch","to":"final title"}`,
			mutate:  func(i *db.Issue) { i.Title = "final title" },
			assert: func(t *testing.T, got db.Issue) {
				if got.Title != "title at branch" {
					t.Errorf("title = %q, want %q", got.Title, "title at branch")
				}
			},
		},
		{
			name:    "priority",
			action:  "priority_changed",
			details: `{"from":"medium","to":"urgent"}`,
			mutate:  func(i *db.Issue) { i.Priority = "urgent" },
			assert: func(t *testing.T, got db.Issue) {
				if got.Priority != "medium" {
					t.Errorf("priority = %q, want medium", got.Priority)
				}
			},
		},
		{
			name:    "due_date",
			action:  "due_date_changed",
			details: `{"from":"2026-05-01","to":"2026-09-09"}`,
			mutate:  func(i *db.Issue) { i.DueDate = mustDate("2026-09-09") },
			assert: func(t *testing.T, got db.Issue) {
				if !got.DueDate.Valid || got.DueDate.Time.Format(time.DateOnly) != "2026-05-01" {
					t.Errorf("due_date = %+v, want 2026-05-01", got.DueDate)
				}
			},
		},
		{
			name:    "assignee restored to a member",
			action:  "assignee_changed",
			details: `{"from_type":"member","from_id":"00000000-0000-0000-0000-0000000000aa","to_type":"agent","to_id":"00000000-0000-0000-0000-0000000000bb"}`,
			mutate: func(i *db.Issue) {
				i.AssigneeType = pgtype.Text{String: "agent", Valid: true}
			},
			assert: func(t *testing.T, got db.Issue) {
				if !got.AssigneeType.Valid || got.AssigneeType.String != "member" {
					t.Errorf("assignee_type = %+v, want member", got.AssigneeType)
				}
				if !got.AssigneeID.Valid {
					t.Errorf("assignee_id should be set, got %+v", got.AssigneeID)
				}
			},
		},
		{
			name:    "assignee restored to unassigned",
			action:  "assignee_changed",
			details: `{"to_type":"agent","to_id":"00000000-0000-0000-0000-0000000000bb"}`,
			mutate: func(i *db.Issue) {
				i.AssigneeType = pgtype.Text{String: "agent", Valid: true}
				i.AssigneeID = uuidByte(7)
			},
			assert: func(t *testing.T, got db.Issue) {
				if got.AssigneeType.Valid {
					t.Errorf("assignee_type should be NULL (unassigned), got %+v", got.AssigneeType)
				}
				if got.AssigneeID.Valid {
					t.Errorf("assignee_id should be NULL, got %+v", got.AssigneeID)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, sourceID, taskID, cutoff := newForkFixture()
			tc.mutate(&f.source)
			f.activities = []db.ActivityLog{
				activity(sourceID, tc.action, tc.details, cutoff.Add(time.Hour)),
				// A second, later change of the same field must be ignored.
				activity(sourceID, tc.action, `{"from":"ignored","from_type":"member","from_id":""}`, cutoff.Add(2*time.Hour)),
			}
			forked, err := NewIssueForkService(f).ForkIssueSubtree(context.Background(), sourceID, taskID, 5)
			if err != nil {
				t.Fatalf("ForkIssueSubtree: %v", err)
			}
			tc.assert(t, forked)
		})
	}
}

func mustDate(s string) pgtype.Date {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return pgtype.Date{Time: t, Valid: true}
}
