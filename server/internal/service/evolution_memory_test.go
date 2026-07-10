package service

import (
	"context"
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

func TestCurateAndMatchWorkspaceMemoryAssigns(t *testing.T) {
	submission := validMemorySubmission()
	submission.Status = "candidate"
	mock := newEvolutionMockDB(submission)
	mock.submissions = []db.EvolutionUnitSubmission{submission}
	service := NewEvolutionService(db.New(mock))

	result, err := service.CurateAndMatchWorkspace(context.Background(), submission.WorkspaceID, 10)
	if err != nil {
		t.Fatalf("CurateAndMatchWorkspace error = %v", err)
	}
	if result.Promoted != 1 || result.Matched != 1 {
		t.Fatalf("result = %+v, want promoted=1 matched=1", result)
	}
	if len(mock.memories) != 1 {
		t.Fatalf("memories = %d, want 1", len(mock.memories))
	}
	if !mock.submission.PromotedUnitID.Valid {
		t.Fatal("promoted unit id is invalid, want shared memory unit")
	}
}
