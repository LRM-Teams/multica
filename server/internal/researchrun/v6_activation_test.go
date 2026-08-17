package researchrun

import (
	"errors"
	"reflect"
	"testing"
)

func completeV6ActivationEvidence() V6ActivationEvidence {
	passed := V6GateEvidence{Passed: true, EvidenceID: "audit/result", Revision: "sha256:verified"}
	return V6ActivationEvidence{
		Migrations:     passed,
		Contract:       passed,
		WorkItems:      passed,
		Director:       passed,
		Team:           passed,
		KnowledgeGraph: passed,
		Discussion:     passed,
		Steering:       passed,
		Projection:     passed,
		GraphClients:   passed,
		ReportSandbox:  passed,
		Compatibility:  passed,
		Rollback: V6RollbackEvidence{
			V6GateEvidence:  passed,
			PreviousVersion: OrchestratorVersionV5,
		},
	}
}

func TestAssessV6ActivationReportsEveryMissingExitInCanonicalOrder(t *testing.T) {
	decision := AssessV6Activation(V6ActivationEvidence{})
	want := []V6ActivationRequirement{
		V6RequirementMigrations,
		V6RequirementContract,
		V6RequirementWorkItems,
		V6RequirementDirector,
		V6RequirementTeam,
		V6RequirementKnowledgeGraph,
		V6RequirementDiscussion,
		V6RequirementSteering,
		V6RequirementProjection,
		V6RequirementGraphClients,
		V6RequirementReportSandbox,
		V6RequirementCompatibility,
		V6RequirementRollback,
	}
	if decision.ActivationAllowed {
		t.Fatal("V6 activation allowed without evidence")
	}
	if !reflect.DeepEqual(decision.Missing, want) {
		t.Fatalf("missing=%v want=%v", decision.Missing, want)
	}
	if decision.CurrentDefault != OrchestratorVersionV5 || decision.CandidateVersion != v6ActivationCandidate {
		t.Fatalf("version decision=%+v", decision)
	}
}

func TestAssessV6ActivationRequiresVersionedEvidenceForEveryExit(t *testing.T) {
	complete := completeV6ActivationEvidence()
	cases := []struct {
		name        string
		requirement V6ActivationRequirement
		remove      func(*V6ActivationEvidence)
	}{
		{"migrations", V6RequirementMigrations, func(e *V6ActivationEvidence) { e.Migrations.Passed = false }},
		{"contract", V6RequirementContract, func(e *V6ActivationEvidence) { e.Contract.EvidenceID = "" }},
		{"work items", V6RequirementWorkItems, func(e *V6ActivationEvidence) { e.WorkItems.Revision = "" }},
		{"director", V6RequirementDirector, func(e *V6ActivationEvidence) { e.Director.Passed = false }},
		{"team", V6RequirementTeam, func(e *V6ActivationEvidence) { e.Team.Passed = false }},
		{"knowledge graph", V6RequirementKnowledgeGraph, func(e *V6ActivationEvidence) { e.KnowledgeGraph.Passed = false }},
		{"discussion", V6RequirementDiscussion, func(e *V6ActivationEvidence) { e.Discussion.Passed = false }},
		{"steering", V6RequirementSteering, func(e *V6ActivationEvidence) { e.Steering.Passed = false }},
		{"projection", V6RequirementProjection, func(e *V6ActivationEvidence) { e.Projection.Passed = false }},
		{"graph clients", V6RequirementGraphClients, func(e *V6ActivationEvidence) { e.GraphClients.Passed = false }},
		{"report sandbox", V6RequirementReportSandbox, func(e *V6ActivationEvidence) { e.ReportSandbox.Passed = false }},
		{"compatibility", V6RequirementCompatibility, func(e *V6ActivationEvidence) { e.Compatibility.Passed = false }},
		{"rollback", V6RequirementRollback, func(e *V6ActivationEvidence) { e.Rollback.PreviousVersion = OrchestratorVersionV4 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evidence := complete
			tc.remove(&evidence)
			decision := AssessV6Activation(evidence)
			if decision.ActivationAllowed || !reflect.DeepEqual(decision.Missing, []V6ActivationRequirement{tc.requirement}) {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestAssessV6ActivationCompleteAuditDoesNotEnableRuntimeV6(t *testing.T) {
	decision := AssessV6Activation(completeV6ActivationEvidence())
	if !decision.ActivationAllowed || len(decision.Missing) != 0 {
		t.Fatalf("decision=%+v", decision)
	}
	if decision.RollbackVersion != OrchestratorVersionV5 {
		t.Fatalf("rollback=%q", decision.RollbackVersion)
	}
	if OrchestratorVersion != OrchestratorVersionV5 {
		t.Fatalf("default orchestrator=%q", OrchestratorVersion)
	}
	if err := ensureSupportedOrchestratorVersion(v6ActivationCandidate); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("V6 runtime support changed through readiness audit: %v", err)
	}
}
