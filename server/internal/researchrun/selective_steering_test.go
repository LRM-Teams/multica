package researchrun

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	steeringRootA  = "10000000-0000-4000-8000-000000000001"
	steeringChildA = "10000000-0000-4000-8000-000000000002"
	steeringRootB  = "10000000-0000-4000-8000-000000000003"
	steeringTaskA  = "20000000-0000-4000-8000-000000000001"
	steeringTaskAA = "20000000-0000-4000-8000-000000000002"
	steeringTaskB  = "20000000-0000-4000-8000-000000000003"
	steeringDoneA  = "20000000-0000-4000-8000-000000000004"
)

func validSelectiveSteeringState() selectiveSteeringState {
	return selectiveSteeringState{
		StateVersion: 9,
		Branches: []steeringBranchState{
			{ID: steeringRootA, Status: "active"},
			{ID: steeringChildA, ParentID: steeringRootA, Status: "active"},
			{ID: steeringRootB, Status: "active"},
		},
		Tasks: []steeringTaskState{
			{ID: steeringTaskA, Status: TaskStatusReady, BranchIDs: []string{steeringRootA}},
			{ID: steeringTaskAA, Status: TaskStatusRunning, BranchIDs: []string{steeringChildA}},
			{ID: steeringTaskB, Status: TaskStatusPending, BranchIDs: []string{steeringRootB}},
			{ID: steeringDoneA, Status: TaskStatusSucceeded, BranchIDs: []string{steeringRootA}},
		},
	}
}

func validSelectiveSteerInput() SteerInput {
	return SteerInput{
		WorkspaceID: "30000000-0000-4000-8000-000000000001",
		SessionID:   "30000000-0000-4000-8000-000000000002",
		UserID:      "30000000-0000-4000-8000-000000000003",
		Reason:      "Redirect this branch after contradictory evidence.", ExpectedStateVersion: 9,
		AffectedBranchIDs: []string{steeringRootB, steeringRootA},
	}
}

func TestValidateSelectiveSteerInput(t *testing.T) {
	if err := validateSelectiveSteerInput(validSelectiveSteerInput()); err != nil {
		t.Fatalf("valid selective steer rejected: %v", err)
	}
	full := validSelectiveSteerInput()
	full.AffectedBranchIDs = nil
	full.FullReplan = true
	if err := validateSelectiveSteerInput(full); err != nil {
		t.Fatalf("valid full replan rejected: %v", err)
	}
	for _, mutate := range []func(*SteerInput){
		func(in *SteerInput) { in.ExpectedStateVersion = 0 },
		func(in *SteerInput) { in.Reason = "" },
		func(in *SteerInput) { in.FullReplan = true },
		func(in *SteerInput) { in.AffectedBranchIDs = nil },
		func(in *SteerInput) { in.AffectedBranchIDs = append(in.AffectedBranchIDs, in.AffectedBranchIDs[0]) },
		func(in *SteerInput) { in.AffectedBranchIDs[0] = "branch.one" },
	} {
		in := validSelectiveSteerInput()
		mutate(&in)
		if err := validateSelectiveSteerInput(in); !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("error=%v, want ErrInvalidContract", err)
		}
	}
}

func TestSelectiveSteeringIdempotencyKeyIsSemantic(t *testing.T) {
	in := validSelectiveSteerInput()
	request := canonicalSelectiveSteeringRequest(in)
	key, err := selectiveSteeringIdempotencyKey(in.UserID, request)
	if err != nil {
		t.Fatal(err)
	}
	reversed := validSelectiveSteerInput()
	reversed.AffectedBranchIDs[0], reversed.AffectedBranchIDs[1] = reversed.AffectedBranchIDs[1], reversed.AffectedBranchIDs[0]
	replayKey, err := selectiveSteeringIdempotencyKey(reversed.UserID, canonicalSelectiveSteeringRequest(reversed))
	if err != nil || replayKey != key {
		t.Fatalf("semantic replay key=%q want=%q err=%v", replayKey, key, err)
	}
}

func TestApplySelectiveSteeringTransactionRecovery(t *testing.T) {
	source, err := os.ReadFile("postgres_selective_steering.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := inspectTransactionBoundaryCalls(t, source, "ApplySelectiveSteering")
	if len(calls.direct) != 0 || calls.runner["beginResearchTx"] != 1 || calls.runner["commitResearchTx"] != 2 {
		t.Fatalf("transaction boundaries=%+v", calls)
	}
}

func TestSelectiveSteeringPersistenceUsesExplicitScopeAndPreservesEvidence(t *testing.T) {
	source, err := os.ReadFile("postgres_selective_steering.go")
	if err != nil {
		t.Fatal(err)
	}
	contents := string(source)
	for _, required := range []string{
		"id::text=ANY($3::text[])",
		"plan.ObsoleteBranchIDs",
		"plan.ObsoleteTaskIDs",
		"plan.CancelRunningTaskIDs",
		"state_version=state_version+1",
		"state_version=$3",
		"'selective_steering'",
		"\"selective_steering_applied\"",
	} {
		if !strings.Contains(contents, required) {
			t.Fatalf("selective steering persistence missing %q", required)
		}
	}
	for _, forbidden := range []string{"DELETE FROM research_source", "DELETE FROM research_claim", "DELETE FROM research_observation", "DELETE FROM research_artifact"} {
		if strings.Contains(contents, forbidden) {
			t.Fatalf("selective steering must preserve accepted Evidence: found %q", forbidden)
		}
	}
}

func TestSelectiveSteeringPlanOnlyInvalidatesAffectedBranchClosure(t *testing.T) {
	plan, err := (selectiveSteeringModule{}).Plan(selectiveSteeringRequest{
		ExpectedStateVersion: 9, AffectedBranchIDs: []string{steeringRootA},
	}, validSelectiveSteeringState())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !reflect.DeepEqual(plan.ImpactedBranchIDs, []string{steeringRootA, steeringChildA}) || !reflect.DeepEqual(plan.ObsoleteBranchIDs, []string{steeringRootA, steeringChildA}) {
		t.Fatalf("branches=%+v", plan)
	}
	if !reflect.DeepEqual(plan.ObsoleteTaskIDs, []string{steeringTaskA}) {
		t.Fatalf("obsolete=%v", plan.ObsoleteTaskIDs)
	}
	if !reflect.DeepEqual(plan.CancelRunningTaskIDs, []string{steeringTaskAA}) {
		t.Fatalf("cancel=%v", plan.CancelRunningTaskIDs)
	}
	if len(plan.RetainedRunningTaskIDs) != 0 {
		t.Fatalf("retained=%v", plan.RetainedRunningTaskIDs)
	}
	// Task B is unaffected and the succeeded Task remains immutable history.
	for _, unexpected := range []string{steeringTaskB, steeringDoneA} {
		if containsSteeringID(plan.ObsoleteTaskIDs, unexpected) || containsSteeringID(plan.CancelRunningTaskIDs, unexpected) {
			t.Fatalf("unaffected/history task %s was selected", unexpected)
		}
	}
}

func TestSelectiveSteeringPlanCanRetainAffectedRunningWork(t *testing.T) {
	plan, err := (selectiveSteeringModule{}).Plan(selectiveSteeringRequest{
		ExpectedStateVersion: 9, AffectedBranchIDs: []string{steeringRootA}, AllowRunningFinish: true,
	}, validSelectiveSteeringState())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.RetainedRunningTaskIDs, []string{steeringTaskAA}) || len(plan.CancelRunningTaskIDs) != 0 {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestSelectiveSteeringPlanFullReplanSelectsAllNonterminalWork(t *testing.T) {
	plan, err := (selectiveSteeringModule{}).Plan(selectiveSteeringRequest{
		ExpectedStateVersion: 9, FullReplan: true,
	}, validSelectiveSteeringState())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.ObsoleteTaskIDs, []string{steeringTaskA, steeringTaskB}) || !reflect.DeepEqual(plan.CancelRunningTaskIDs, []string{steeringTaskAA}) {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestSelectiveSteeringPlanPreservesTerminalBranchHistory(t *testing.T) {
	state := validSelectiveSteeringState()
	state.Branches[0].Status = "completed"
	plan, err := (selectiveSteeringModule{}).Plan(selectiveSteeringRequest{
		ExpectedStateVersion: 9, AffectedBranchIDs: []string{steeringRootA},
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.ImpactedBranchIDs, []string{steeringRootA, steeringChildA}) || !reflect.DeepEqual(plan.ObsoleteBranchIDs, []string{steeringChildA}) {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestSelectiveSteeringPlanFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		request selectiveSteeringRequest
		mutate  func(*selectiveSteeringState)
		want    error
	}{
		{name: "stale state", request: selectiveSteeringRequest{ExpectedStateVersion: 8, AffectedBranchIDs: []string{steeringRootA}}, want: ErrControlTargetChanged},
		{name: "empty selective roots", request: selectiveSteeringRequest{ExpectedStateVersion: 9}, want: ErrInvalidContract},
		{name: "unknown root", request: selectiveSteeringRequest{ExpectedStateVersion: 9, AffectedBranchIDs: []string{"10000000-0000-4000-8000-000000000099"}}, want: ErrInvalidContract},
		{name: "branch cycle", request: selectiveSteeringRequest{ExpectedStateVersion: 9, AffectedBranchIDs: []string{steeringRootA}}, mutate: func(s *selectiveSteeringState) { s.Branches[0].ParentID = steeringChildA }, want: ErrInvalidContract},
		{name: "task branch outside Run", request: selectiveSteeringRequest{ExpectedStateVersion: 9, AffectedBranchIDs: []string{steeringRootA}}, mutate: func(s *selectiveSteeringState) {
			s.Tasks[0].BranchIDs = []string{"10000000-0000-4000-8000-000000000099"}
		}, want: ErrInvalidContract},
		{name: "duplicate task branch", request: selectiveSteeringRequest{ExpectedStateVersion: 9, AffectedBranchIDs: []string{steeringRootA}}, mutate: func(s *selectiveSteeringState) {
			s.Tasks[0].BranchIDs = []string{steeringRootA, steeringRootA}
		}, want: ErrInvalidContract},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := validSelectiveSteeringState()
			if test.mutate != nil {
				test.mutate(&state)
			}
			if _, err := (selectiveSteeringModule{}).Plan(test.request, state); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want %v", err, test.want)
			}
		})
	}
}

func containsSteeringID(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
