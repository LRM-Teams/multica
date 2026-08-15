package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestShouldProjectGateHidesExpectedIntermediateFindings(t *testing.T) {
	finding := researchrun.GateFinding{Code: "plan_incomplete", Message: "The current plan has not been accepted."}
	for _, stage := range []string{"s1_plan", "s2_sources", "s3_validation"} {
		snapshot := researchrun.RunSnapshot{Run: researchrun.Run{Status: researchrun.RunStatusRunning, CurrentStage: stage}, Gate: researchrun.GateResult{Findings: []researchrun.GateFinding{finding}}}
		if shouldProjectGate(snapshot) {
			t.Fatalf("stage %s projected an unevaluated delivery Gate", stage)
		}
	}
}

func TestShouldProjectGateShowsOnlyActionableDeliveryStates(t *testing.T) {
	finding := researchrun.GateFinding{Code: "report_missing", Message: "No report revision exists for delivery."}
	tests := []researchrun.RunSnapshot{
		{Run: researchrun.Run{Status: researchrun.RunStatusRunning, CurrentStage: "s4_delivery"}, Gate: researchrun.GateResult{Findings: []researchrun.GateFinding{finding}}},
		{Run: researchrun.Run{Status: researchrun.RunStatusAwaitingUserConfirm, CurrentStage: "s4_delivery"}},
		{Run: researchrun.Run{Status: researchrun.RunStatusCompleted, CurrentStage: "s4_delivery"}, Gate: researchrun.GateResult{Passed: true}},
	}
	for i, snapshot := range tests {
		if !shouldProjectGate(snapshot) {
			t.Fatalf("actionable Gate case %d was hidden", i)
		}
	}
}

func TestProjectRunV2GraphOmitsPlanningReadinessGate(t *testing.T) {
	snapshot := researchrun.RunSnapshot{
		Run:  researchrun.Run{SessionID: "run-1", Status: researchrun.RunStatusRunning, CurrentStage: "s1_plan"},
		Gate: researchrun.GateResult{Findings: []researchrun.GateFinding{{Code: "plan_incomplete", Message: "The current plan has not been accepted."}}},
	}
	nodes, _ := projectRunV2Graph(snapshot)
	for _, node := range nodes {
		if node.NodeType == "stage_gate" {
			t.Fatalf("planning snapshot projected Gate node: %+v", node)
		}
	}
}
