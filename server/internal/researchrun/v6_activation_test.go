package researchrun

import (
	"errors"
	"reflect"
	"testing"
)

func completeV6ActivationEvidence() V6ActivationEvidence {
	passed := V6GateEvidence{Passed: true, EvidenceID: "audit/result", Revision: "sha256:verified"}
	return V6ActivationEvidence{
		Inquiry:          passed,
		Corpus:           passed,
		Integration:      passed,
		Dispute:          passed,
		Portfolio:        passed,
		TeamFormation:    passed,
		ReportEvaluation: passed,
		Decoder:          passed,
		Persistence:      passed,
		Recovery:         passed,
		Projection:       passed,
		SystemEvaluation: passed,
		ShadowTraffic:    passed,
		Rollback: V6RollbackEvidence{
			V6GateEvidence:  passed,
			PreviousVersion: OrchestratorVersionV5,
		},
	}
}

func TestAssessV6ActivationReportsEveryMissingExitInCanonicalOrder(t *testing.T) {
	decision := AssessV6Activation(V6ActivationEvidence{})
	want := []V6ActivationRequirement{
		V6RequirementInquiry,
		V6RequirementCorpus,
		V6RequirementIntegration,
		V6RequirementDispute,
		V6RequirementPortfolio,
		V6RequirementTeamFormation,
		V6RequirementReportEvaluation,
		V6RequirementDecoder,
		V6RequirementPersistence,
		V6RequirementRecovery,
		V6RequirementProjection,
		V6RequirementSystemEvaluation,
		V6RequirementShadowTraffic,
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
		{"inquiry", V6RequirementInquiry, func(e *V6ActivationEvidence) { e.Inquiry.Passed = false }},
		{"corpus", V6RequirementCorpus, func(e *V6ActivationEvidence) { e.Corpus.EvidenceID = "" }},
		{"integration", V6RequirementIntegration, func(e *V6ActivationEvidence) { e.Integration.Revision = "" }},
		{"dispute", V6RequirementDispute, func(e *V6ActivationEvidence) { e.Dispute.Passed = false }},
		{"portfolio", V6RequirementPortfolio, func(e *V6ActivationEvidence) { e.Portfolio.Passed = false }},
		{"team formation", V6RequirementTeamFormation, func(e *V6ActivationEvidence) { e.TeamFormation.Passed = false }},
		{"report evaluation", V6RequirementReportEvaluation, func(e *V6ActivationEvidence) { e.ReportEvaluation.Passed = false }},
		{"decoder", V6RequirementDecoder, func(e *V6ActivationEvidence) { e.Decoder.Passed = false }},
		{"persistence", V6RequirementPersistence, func(e *V6ActivationEvidence) { e.Persistence.Passed = false }},
		{"recovery", V6RequirementRecovery, func(e *V6ActivationEvidence) { e.Recovery.Passed = false }},
		{"projection", V6RequirementProjection, func(e *V6ActivationEvidence) { e.Projection.Passed = false }},
		{"system evaluation", V6RequirementSystemEvaluation, func(e *V6ActivationEvidence) { e.SystemEvaluation.Passed = false }},
		{"shadow traffic", V6RequirementShadowTraffic, func(e *V6ActivationEvidence) { e.ShadowTraffic.Passed = false }},
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
