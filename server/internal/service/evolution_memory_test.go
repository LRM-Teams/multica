package service

import (
	"context"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCurateMemorySubmissionAssigns(t *testing.T) {
	submission := validMemorySubmission()
	mock := newEvolutionMockDB(submission)
	service := NewEvolutionService(db.New(mock))

	status, err := service.curateMemorySubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateMemorySubmission error = %v", err)
	}
	if status != evolutionCurationPromoted || mock.submission.Status != "promoted" {
		t.Fatalf("status/submission = %q/%q, want promoted/promoted", status, mock.submission.Status)
	}
	if len(mock.memories) != 1 {
		t.Fatalf("memories = %d, want 1", len(mock.memories))
	}
	if !mock.submission.PromotedUnitID.Valid {
		t.Fatal("promoted unit id is invalid, want shared memory unit")
	}
	if mock.memories[0].Content != submission.Content {
		t.Fatalf("memory content mismatch")
	}
}

func TestCurateMemorySubmissionUpdatesExistingSyncKey(t *testing.T) {
	submission := validMemorySubmission()
	mock := newEvolutionMockDB(submission)
	syncKey := "evolution/memory/memory-1"
	mock.memories = []db.AgentMemory{{
		ID:          testUUID(90),
		WorkspaceID: submission.WorkspaceID,
		AgentID:     submission.SourceAgentID,
		Name:        submission.Title,
		Content:     "old content",
		SyncKey:     syncKey,
		ContentHash: "old-hash",
	}}
	service := NewEvolutionService(db.New(mock))

	status, err := service.curateMemorySubmission(context.Background(), submission)
	if err != nil {
		t.Fatalf("curateMemorySubmission error = %v", err)
	}
	if status != evolutionCurationPromoted {
		t.Fatalf("status = %q, want promoted", status)
	}
	if mock.memories[0].Content != submission.Content {
		t.Fatalf("memory content = %q, want updated content", mock.memories[0].Content)
	}
}

func TestCurateAndMatchWorkspaceRequiresTransactions(t *testing.T) {
	submission := validMemorySubmission()
	submission.Status = "candidate"
	mock := newEvolutionMockDB(submission)
	mock.submissions = []db.EvolutionUnitSubmission{submission}
	service := NewEvolutionService(db.New(mock))

	_, err := service.CurateAndMatchWorkspace(context.Background(), submission.WorkspaceID, 10)
	if err == nil || !strings.Contains(err.Error(), "requires transaction support") {
		t.Fatalf("CurateAndMatchWorkspace error = %v, want transaction support error", err)
	}
}
