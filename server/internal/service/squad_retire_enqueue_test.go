package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestEnqueueTaskForSquadLeaderRetired(t *testing.T) {
	s := &TaskService{}
	_, err := s.EnqueueTaskForSquadLeader(context.Background(), db.Issue{}, pgtype.UUID{}, pgtype.UUID{})
	if err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("EnqueueTaskForSquadLeader want retired error, got %v", err)
	}
}

// TestDispatchCreateIssueSquadRejectsBeforeDB proves the entry guard runs with
// a nil Queries/TxStarter — if it reached any DB call it would panic.
func TestDispatchCreateIssueSquadRejectsBeforeDB(t *testing.T) {
	s := &AutopilotService{} // nil Queries / TxStarter
	ap := db.Autopilot{AssigneeType: "squad"}
	run := &db.AutopilotRun{}
	err := s.dispatchCreateIssue(context.Background(), ap, run, "")
	if err == nil || !strings.Contains(err.Error(), "squad autopilots retired") {
		t.Fatalf("dispatchCreateIssue want retired error before DB, got %v", err)
	}
}

// TestDispatchRunOnlySquadRejectsBeforeDB same contract for run_only path.
func TestDispatchRunOnlySquadRejectsBeforeDB(t *testing.T) {
	s := &AutopilotService{}
	ap := db.Autopilot{AssigneeType: "squad"}
	run := &db.AutopilotRun{}
	err := s.dispatchRunOnly(context.Background(), ap, run)
	var skipped *errDispatchSkipped
	if !errors.As(err, &skipped) {
		t.Fatalf("dispatchRunOnly want errDispatchSkipped, got %v", err)
	}
	if !strings.Contains(skipped.reason, "squad autopilots retired") {
		t.Fatalf("skip reason = %q, want retired", skipped.reason)
	}
}
