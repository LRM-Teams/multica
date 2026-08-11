package researchrun

import (
	"slices"
	"testing"
)

func TestMigrationArtifactContentHashMatchesSQL(t *testing.T) {
	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000002"
	entityID := "30000000-0000-4000-8000-000000000003"
	got := migrationArtifactContentHash(ArtifactKindTask, workspaceID, sessionID, entityID)
	want := "sha256:89a0eb387df6af5e0f77bcfe0452a48fd924d7377d69ff9b0e26d9afda5d47cf"
	if got != want {
		t.Fatalf("hash=%q want=%q", got, want)
	}
}

func TestRegisteredArtifactEntityKindsMatchSpecInventory(t *testing.T) {
	want := []ArtifactEntityKind{
		ArtifactKindRunSession,
		ArtifactKindContractRevision,
		ArtifactKindMethodDecision,
		ArtifactKindQuestion,
		ArtifactKindTask,
		ArtifactKindAttempt,
		ArtifactKindResultArtifact,
		ArtifactKindLegacySource,
		ArtifactKindSourceSnapshot,
		ArtifactKindObservation,
		ArtifactKindClaim,
		ArtifactKindEvidenceLink,
		ArtifactKindReportRevision,
		ArtifactKindEvaluationDecision,
		ArtifactKindStageEvaluation,
		ArtifactKindResearchMessage,
		ArtifactKindProductRoundDecision,
		ArtifactKindContextManifest,
		ArtifactKindRunEvent,
		ArtifactKindGraphNode,
		ArtifactKindGraphEdge,
	}
	got := RegisteredArtifactEntityKinds()
	slices.SortFunc(got, func(a, b ArtifactEntityKind) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	slices.SortFunc(want, func(a, b ArtifactEntityKind) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	if !slices.Equal(got, want) {
		t.Fatalf("registered kinds=%v want=%v", got, want)
	}
}

func TestParseArtifactEntityKindRejectsUnknown(t *testing.T) {
	if _, err := ParseArtifactEntityKind("hypothesis"); err == nil {
		t.Fatal("expected unknown kind error")
	}
}

func TestParseArtifactEntityKindAcceptsRegistered(t *testing.T) {
	kind, err := ParseArtifactEntityKind(string(ArtifactKindTask))
	if err != nil || kind != ArtifactKindTask {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
}

func TestReciprocalArtifactPassportGuardTriggerNames(t *testing.T) {
	want := []string{
		"research_session_artifact_passport_guard",
		"research_contract_revision_artifact_passport_guard",
		"research_decision_artifact_passport_guard",
		"research_question_artifact_passport_guard",
		"research_task_artifact_passport_guard",
		"research_task_attempt_artifact_passport_guard",
		"research_result_artifact_artifact_passport_guard",
		"research_source_artifact_passport_guard",
		"research_source_snapshot_artifact_passport_guard",
		"research_observation_artifact_passport_guard",
		"research_claim_artifact_passport_guard",
		"research_claim_evidence_artifact_passport_guard",
		"research_report_artifact_passport_guard",
		"research_stage_eval_artifact_passport_guard",
		"research_message_artifact_passport_guard",
		"research_product_round_card_artifact_passport_guard",
		"research_artifact_context_manifest_artifact_passport_guard",
		"research_run_event_artifact_passport_guard",
		"research_graph_node_artifact_passport_guard",
		"research_graph_edge_artifact_passport_guard",
	}
	got := ReciprocalArtifactPassportGuardTriggerNames()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("guard triggers=%v want=%v", got, want)
	}
}

func TestPolicyCouplingGuardTriggerNames(t *testing.T) {
	want := []string{
		"research_source_snapshot_verification_to_policy_guard",
		"research_observation_verification_to_policy_guard",
		"research_claim_evidence_verification_to_policy_guard",
		"research_artifact_policy_mutation_to_verification_guard",
		"research_artifact_policy_grant_to_mutation_guard",
		"research_artifact_policy_mutation_to_grant_guard",
	}
	got := PolicyCouplingGuardTriggerNames()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("policy coupling triggers=%v want=%v", got, want)
	}
}
