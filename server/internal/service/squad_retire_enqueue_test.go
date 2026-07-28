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
