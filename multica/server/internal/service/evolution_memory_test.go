package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCurateMemorySubmissionAssigns(t *testing.T) {
	submission := validMemorySubmission()
	submission.Applies = json.RawMessage(`{"project_ids":["project-1"],"channel_ids":["channel-1"],"task_types":["chat"],"expires_at":"2099-01-01T00:00:00Z"}`)
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
	var delivery agentMemoryDeliveryConfig
	if err := json.Unmarshal(mock.memories[0].Config, &delivery); err != nil {
		t.Fatal(err)
	}
	if delivery.Scope != "workspace" {
		t.Fatalf("memory delivery scope = %q, want workspace", delivery.Scope)
	}
	if len(delivery.Applies.ProjectIDs) != 1 || delivery.Applies.ProjectIDs[0] != "project-1" {
		t.Fatalf("memory delivery project applicability = %#v", delivery.Applies.ProjectIDs)
	}
	if len(delivery.Applies.ChannelIDs) != 1 || delivery.Applies.ChannelIDs[0] != "channel-1" {
		t.Fatalf("memory delivery channel applicability = %#v", delivery.Applies.ChannelIDs)
	}
	if len(delivery.Applies.TaskTypes) != 1 || delivery.Applies.TaskTypes[0] != "chat" {
		t.Fatalf("memory delivery task applicability = %#v", delivery.Applies.TaskTypes)
	}
	if delivery.Applies.ExpiresAt != "2099-01-01T00:00:00Z" {
		t.Fatalf("memory delivery expiry = %q", delivery.Applies.ExpiresAt)
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

func TestEvolutionMemoryDeliveryScopeRequiresStableMember(t *testing.T) {
	submission := validMemorySubmission()
	submission.UnitType = "preference"
	submission.SuggestedScope = "user"
	if _, _, _, err := evolutionMemoryDeliveryScope(submission); err == nil {
		t.Fatal("user-scoped preference without member subject was accepted")
	}
	submission.Payload = json.RawMessage(`{"subject_type":"member","subject_id":"11111111-1111-1111-1111-111111111111"}`)
	scope, subjectType, subjectID, err := evolutionMemoryDeliveryScope(submission)
	if err != nil {
		t.Fatal(err)
	}
	if scope != "user" || subjectType != "member" || subjectID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("delivery = %q/%q/%q", scope, subjectType, subjectID)
	}
	if !evolutionSubmissionRequiresHumanReview(submission) {
		t.Fatal("user-scoped preference must require human review")
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
