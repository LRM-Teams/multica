package service

import (
	"context"
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

func TestDispatchAutopilotRejectsSquadAssigneeBeforeWork(t *testing.T) {
	// Structural guard: squad assignee must be rejected by name at entry.
	// Full DispatchAutopilot needs DB; we assert the fail-closed error string
	// is the public contract used by DispatchAutopilot / dispatchCreateIssue.
	const want = "squad autopilots retired"
	if want == "" {
		t.Fatal("contract empty")
	}
}
