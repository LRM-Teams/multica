package researchrun

import (
	"errors"
	"reflect"
	"testing"
)

func completeV6ActivationEvidence() V6ActivationEvidence {
	p := V6GateEvidence{Passed: true, EvidenceID: "audit/result", Revision: "sha256:verified"}
	return V6ActivationEvidence{
		Migrations: p, SchemaHash: p, LegacyGolden: p, NineEnvelopes: p,
		RecoveryMatrix: p, SingleSuccessorRace: p, DirectorContext: p, TeamLimit: p,
		KnowledgeGraph: p, Discussion: p, Steering: p, ProjectionRebuild: p,
		ProjectionScale: p, GraphClients: p, ReportSandboxWeb: p, ReportSandboxDesktop: p,
		ReportOrigin: V6ReportOriginEvidence{V6GateEvidence: p, ReportOrigin: "https://reports.example.test", ApplicationOrigins: []string{"https://app.example.test", "https://desktop.example.test"}},
		BuiltinDocs:  p,
		Rollback:     V6RollbackEvidence{V6GateEvidence: p, PreviousVersion: OrchestratorVersionV5},
	}
}

var canonicalV6ActivationRequirements = []V6ActivationRequirement{
	V6RequirementMigrations, V6RequirementSchemaHash, V6RequirementLegacyGolden,
	V6RequirementNineEnvelopes, V6RequirementRecoveryMatrix, V6RequirementSingleSuccessorRace,
	V6RequirementDirectorContext, V6RequirementTeamLimit, V6RequirementKnowledgeGraph,
	V6RequirementDiscussion, V6RequirementSteering, V6RequirementProjectionRebuild,
	V6RequirementProjectionScale, V6RequirementGraphClients, V6RequirementReportSandboxWeb,
	V6RequirementReportSandboxDesktop, V6RequirementReportOrigin, V6RequirementBuiltinDocs,
	V6RequirementRollback,
}

func TestAssessV6ActivationReportsEveryMissingExitInCanonicalOrder(t *testing.T) {
	decision := AssessV6Activation(V6ActivationEvidence{})
	if decision.ActivationAllowed || !reflect.DeepEqual(decision.Missing, canonicalV6ActivationRequirements) {
		t.Fatalf("decision=%+v", decision)
	}
	if decision.CurrentDefault != OrchestratorVersionV5 || decision.CandidateVersion != v6ActivationCandidate {
		t.Fatalf("version decision=%+v", decision)
	}
}

func TestAssessV6ActivationRequiresVersionedEvidenceForEveryExit(t *testing.T) {
	complete := completeV6ActivationEvidence()
	for _, requirement := range canonicalV6ActivationRequirements {
		t.Run(string(requirement), func(t *testing.T) {
			e := complete
			gateForV6Requirement(&e, requirement).Passed = false
			decision := AssessV6Activation(e)
			if decision.ActivationAllowed || !reflect.DeepEqual(decision.Missing, []V6ActivationRequirement{requirement}) {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func gateForV6Requirement(e *V6ActivationEvidence, requirement V6ActivationRequirement) *V6GateEvidence {
	switch requirement {
	case V6RequirementMigrations:
		return &e.Migrations
	case V6RequirementSchemaHash:
		return &e.SchemaHash
	case V6RequirementLegacyGolden:
		return &e.LegacyGolden
	case V6RequirementNineEnvelopes:
		return &e.NineEnvelopes
	case V6RequirementRecoveryMatrix:
		return &e.RecoveryMatrix
	case V6RequirementSingleSuccessorRace:
		return &e.SingleSuccessorRace
	case V6RequirementDirectorContext:
		return &e.DirectorContext
	case V6RequirementTeamLimit:
		return &e.TeamLimit
	case V6RequirementKnowledgeGraph:
		return &e.KnowledgeGraph
	case V6RequirementDiscussion:
		return &e.Discussion
	case V6RequirementSteering:
		return &e.Steering
	case V6RequirementProjectionRebuild:
		return &e.ProjectionRebuild
	case V6RequirementProjectionScale:
		return &e.ProjectionScale
	case V6RequirementGraphClients:
		return &e.GraphClients
	case V6RequirementReportSandboxWeb:
		return &e.ReportSandboxWeb
	case V6RequirementReportSandboxDesktop:
		return &e.ReportSandboxDesktop
	case V6RequirementReportOrigin:
		return &e.ReportOrigin.V6GateEvidence
	case V6RequirementBuiltinDocs:
		return &e.BuiltinDocs
	case V6RequirementRollback:
		return &e.Rollback.V6GateEvidence
	default:
		panic("unknown V6 activation requirement")
	}
}

func TestAssessV6ActivationRejectsSameOrInvalidReportOrigin(t *testing.T) {
	for _, origin := range []string{"http://reports.example.test", "https://app.example.test", "https://app.example.test:443/", "https://reports.example.test/path"} {
		e := completeV6ActivationEvidence()
		e.ReportOrigin.ReportOrigin = origin
		decision := AssessV6Activation(e)
		if !reflect.DeepEqual(decision.Missing, []V6ActivationRequirement{V6RequirementReportOrigin}) {
			t.Fatalf("origin=%q decision=%+v", origin, decision)
		}
	}
}

func TestAssessV6ActivationCompleteAuditDoesNotEnableRuntimeV6(t *testing.T) {
	decision := AssessV6Activation(completeV6ActivationEvidence())
	if !decision.ActivationAllowed || decision.RollbackVersion != OrchestratorVersionV5 {
		t.Fatalf("decision=%+v", decision)
	}
	if OrchestratorVersion != OrchestratorVersionV5 {
		t.Fatalf("default orchestrator=%q", OrchestratorVersion)
	}
	if err := ensureSupportedOrchestratorVersion(v6ActivationCandidate); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("V6 runtime support changed through readiness audit: %v", err)
	}
}
