package researchrun

import (
	"errors"
	"reflect"
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
