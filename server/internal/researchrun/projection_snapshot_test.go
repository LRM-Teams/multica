package researchrun

import (
	"context"
	"reflect"
	"testing"
)

type projectionSnapshotTestStore struct {
	run       Run
	contract  ResearchContract
	method    *ResearchMethod
	questions []Question
	tasks     []Task
	attempts  []Attempt
	claims    []Claim
	gate      GateResult

	sessionID   string
	workspaceID string
}

func (s *projectionSnapshotTestStore) GetRun(_ context.Context, sessionID, workspaceID string) (Run, error) {
	s.sessionID = sessionID
	s.workspaceID = workspaceID
	return s.run, nil
}

func (s *projectionSnapshotTestStore) GetCurrentContract(_ context.Context, sessionID, workspaceID string) (ResearchContract, error) {
	s.sessionID = sessionID
	s.workspaceID = workspaceID
	return s.contract, nil
}

func (s *projectionSnapshotTestStore) GetCurrentMethod(_ context.Context, sessionID, workspaceID string) (*ResearchMethod, error) {
	s.sessionID = sessionID
	s.workspaceID = workspaceID
	return s.method, nil
}

func (s *projectionSnapshotTestStore) ListQuestions(_ context.Context, sessionID string) ([]Question, error) {
	s.sessionID = sessionID
	return s.questions, nil
}

func (s *projectionSnapshotTestStore) ListTasks(_ context.Context, sessionID string) ([]Task, error) {
	s.sessionID = sessionID
	return s.tasks, nil
}

func (s *projectionSnapshotTestStore) ListAttempts(_ context.Context, sessionID string) ([]Attempt, error) {
	s.sessionID = sessionID
	return s.attempts, nil
}

func (s *projectionSnapshotTestStore) ListClaims(_ context.Context, sessionID string) ([]Claim, error) {
	s.sessionID = sessionID
	return s.claims, nil
}

func (s *projectionSnapshotTestStore) EvaluateGate(_ context.Context, sessionID string) (GateResult, error) {
	s.sessionID = sessionID
	return s.gate, nil
}

var _ projectionSnapshotStore = (*projectionSnapshotTestStore)(nil)

func TestLoadProjectionSnapshotUsesOnlyProjectionReadModel(t *testing.T) {
	method := &ResearchMethod{}
	store := &projectionSnapshotTestStore{
		run:       Run{SessionID: "session-1", WorkspaceID: "workspace-1"},
		contract:  ResearchContract{},
		method:    method,
		questions: []Question{{ID: "question-1"}},
		tasks:     []Task{{ID: "task-1"}},
		attempts:  []Attempt{{ID: "attempt-1"}},
		claims:    []Claim{{ID: "claim-1"}},
		gate:      GateResult{Passed: true},
	}

	snapshot, err := loadProjectionSnapshot(context.Background(), store, "session-1", "workspace-1")
	if err != nil {
		t.Fatalf("load projection snapshot: %v", err)
	}
	if store.sessionID != "session-1" || store.workspaceID != "workspace-1" {
		t.Fatalf("unexpected scope: session=%q workspace=%q", store.sessionID, store.workspaceID)
	}
	if snapshot.Run != store.run || !reflect.DeepEqual(snapshot.Contract, store.contract) || snapshot.Method != method {
		t.Fatalf("projection identity fields do not match store values: %#v", snapshot)
	}
	if !reflect.DeepEqual(snapshot.Questions, store.questions) ||
		!reflect.DeepEqual(snapshot.Tasks, store.tasks) ||
		!reflect.DeepEqual(snapshot.Attempts, store.attempts) ||
		!reflect.DeepEqual(snapshot.Claims, store.claims) ||
		!reflect.DeepEqual(snapshot.Gate, store.gate) {
		t.Fatalf("projection graph fields do not match store values: %#v", snapshot)
	}

	if snapshot.Sources != nil || snapshot.Observations != nil || snapshot.EvaluationPrivate != nil ||
		snapshot.LegacyContext != nil || snapshot.AttemptContext != nil || snapshot.ArtifactProjection != nil ||
		snapshot.PrincipalHeader != nil {
		t.Fatalf("projection snapshot exposed a forbidden surface: %#v", snapshot)
	}
}
