package researchrun

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type steeringV6StoreStub struct{ called bool }

func (s *steeringV6StoreStub) ApplyV6SteeringAssessment(_ context.Context, in ApplyV6SteeringAssessmentInput) (V6SteeringAssessment, error) {
	s.called = true
	return V6SteeringAssessment{Kind: in.AssessmentKind}, nil
}

func assertV6SteeringSourceContains(t *testing.T, file string, fragments ...string) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range fragments {
		if !strings.Contains(string(raw), fragment) {
			t.Fatalf("%s missing %q", file, fragment)
		}
	}
}

func TestV6SteeringAssessmentTransactionBoundary(t *testing.T) {
	assertV6SteeringSourceContains(t, "postgres_steering_v6.go", "lockRunForMutation", "research_steering_assessment", "v6_steering_assessment_applied", "commitResearchTx")
}

func TestV6SteeringTriggerTransactionBoundary(t *testing.T) {
	assertV6SteeringSourceContains(t, "steering_trigger_v6.go", "research_v6_steering_trigger", "v6_steering_message_received", "directorBriefModule")
}

func TestV6DirectorProposalClaimTransactionBoundary(t *testing.T) {
	assertV6SteeringSourceContains(t, "postgres_director_action_v6.go", "FOR UPDATE OF sub SKIP LOCKED", "status='processing'", "validateV6DirectorActionDAG")
}

func TestV6DirectorProposalCompletionTransactionBoundary(t *testing.T) {
	assertV6SteeringSourceContains(t, "postgres_director_action_v6.go", "research_director_cycle SET status='applied'", "research_work_item_attempt SET status='succeeded'", "research_v6_work_submission SET status='accepted'")
}

func TestV6SteeringNoOpCannotHideMutation(t *testing.T) {
	store := &steeringV6StoreStub{}
	_, err := (steeringV6Module{store: store}).Apply(context.Background(), ApplyV6SteeringAssessmentInput{
		MessageID: "message", DirectorCycleID: "cycle", AssessmentKind: "no_op", Interpretation: "No change", Reason: "Current plan covers it",
		ExpectedGoalVersion: 1, ExpectedStateVersion: 1, Impacts: []V6SteeringImpact{{Kind: "branch", ID: "branch", Reason: "hidden mutation"}},
	})
	if !errors.Is(err, ErrInvalidContract) || store.called {
		t.Fatalf("err=%v called=%v", err, store.called)
	}
}

func TestV6SteeringRejectsDuplicateImpact(t *testing.T) {
	store := &steeringV6StoreStub{}
	impact := V6SteeringImpact{Kind: "branch", ID: "branch", Disposition: "terminate", Reason: "explicit"}
	_, err := (steeringV6Module{store: store}).Apply(context.Background(), ApplyV6SteeringAssessmentInput{
		MessageID: "message", DirectorCycleID: "cycle", AssessmentKind: "local_change", Interpretation: "Stop", Reason: "User correction",
		ExpectedGoalVersion: 1, ExpectedStateVersion: 1, Impacts: []V6SteeringImpact{impact, impact},
	})
	if !errors.Is(err, ErrInvalidContract) || store.called {
		t.Fatalf("err=%v called=%v", err, store.called)
	}
}

func TestV6DirectorActionDAGFailsClosed(t *testing.T) {
	_, err := validateV6DirectorActionDAG([]v6DirectorAction{{ActionID: "a", Kind: "record_decision", IdempotencyKey: "a", PayloadSchema: "steering_assessment.v1", DependsOnActionIDs: []string{"missing"}}})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("err=%v", err)
	}
}
