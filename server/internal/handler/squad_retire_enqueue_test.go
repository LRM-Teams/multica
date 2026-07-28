package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Squad product retired: all enqueue gates must stay hard-closed so historical
// assignee_type=squad cannot wake leaders (Ronan #805 B1–B3).
func TestSquadEnqueueGatesHardClosed(t *testing.T) {
	h := &Handler{}
	issue := db.Issue{
		AssigneeType: pgtype.Text{String: "squad", Valid: true},
		AssigneeID:   pgtype.UUID{Valid: true},
		Status:       "todo",
	}
	ctx := context.Background()
	if h.shouldEnqueueSquadLeaderOnAssign(ctx, issue) {
		t.Fatal("shouldEnqueueSquadLeaderOnAssign must be false after squad retirement")
	}
	if h.isSquadLeaderReady(ctx, issue) {
		t.Fatal("isSquadLeaderReady must be false (covers backlog→active promotion)")
	}
	// enqueue must be a no-op (no panic)
	h.enqueueSquadLeaderTask(ctx, issue, pgtype.UUID{}, "user", "00000000-0000-0000-0000-000000000001")
	if _, ok := h.computeAssignedSquadLeaderCommentTrigger(ctx, issue, "hello", "user", "u1"); ok {
		t.Fatal("computeAssignedSquadLeaderCommentTrigger must not fire")
	}
}
