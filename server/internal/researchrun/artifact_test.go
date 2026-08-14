package researchrun

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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

func TestDecisionRelationshipSchemaRegistryCoversProductionWriters(t *testing.T) {
	registered := make(map[string]bool)
	for _, kind := range DecisionRelationshipSchemaNames() {
		if registered[kind] {
			t.Fatalf("duplicate Decision schema %q", kind)
		}
		registered[kind] = true
	}
	pattern := regexp.MustCompile(`(?s)INSERT INTO research_decision\s*\([^;]{0,500}?\)\s*VALUES\s*\([^;]{0,300}?'([a-z_]+)'`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, readErr := os.ReadFile(file)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, match := range pattern.FindAllSubmatch(source, -1) {
			kind := string(match[1])
			if kind == "agent" || kind == "system" || kind == "user" {
				continue
			}
			if !registered[kind] {
				t.Errorf("%s writes unregistered Decision schema %q", file, kind)
			}
		}
	}
	for _, dynamic := range []string{"quality_gate", "citation_audit"} {
		if !registered[dynamic] {
			t.Errorf("dynamic Decision schema %q is not registered", dynamic)
		}
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "368_research_decision_relationship_schema.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for kind := range registered {
		if !strings.Contains(string(migration), "'"+kind+"'") {
			t.Errorf("migration 368 SQL registry missing Decision schema %q", kind)
		}
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
		ArtifactKindHypothesis,
		ArtifactKindBranch,
		ArtifactKindInsight,
		ArtifactKindIntegrationContribution,
		ArtifactKindIntegrationRound,
		ArtifactKindInquiryEdge,
		ArtifactKindSearchPlan,
		ArtifactKindQueryExecution,
		ArtifactKindSourceCandidate,
		ArtifactKindScreeningDecision,
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
	if _, err := ParseArtifactEntityKind("future_unknown_kind"); err == nil {
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
		"research_hypothesis_artifact_passport_guard",
		"research_branch_artifact_passport_guard",
		"research_insight_artifact_passport_guard",
		"research_inquiry_edge_artifact_passport_guard",
		"research_search_plan_artifact_passport_guard",
		"research_query_execution_artifact_passport_guard",
		"research_source_candidate_artifact_passport_guard",
		"research_screening_decision_artifact_passport_guard",
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

func TestPolicyLedgerGuardTriggerNames(t *testing.T) {
	want := []string{
		"research_artifact_passport_to_policy_mutation_guard",
		"research_artifact_policy_mutation_to_passport_guard",
	}
	got := PolicyLedgerGuardTriggerNames()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("policy ledger triggers=%v want=%v", got, want)
	}
}

func TestIntegrityGuardTriggerNames(t *testing.T) {
	want := []string{
		"research_artifact_version_producer_guard",
		"research_result_attempt_projection_guard",
	}
	got := IntegrityGuardTriggerNames()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("integrity guard triggers=%v want=%v", got, want)
	}
}

func TestLinkPolicyGuardTriggerNames(t *testing.T) {
	want := []string{
		"research_artifact_supersession_cycle_guard",
		"research_artifact_supersession_append_only_guard",
		"research_artifact_supersession_to_policy_guard",
		"research_artifact_policy_mutation_to_supersession_guard",
		"research_artifact_lifecycle_event_to_policy_guard",
		"research_artifact_policy_mutation_to_lifecycle_event_guard",
	}
	got := LinkPolicyGuardTriggerNames()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("link policy guard triggers=%v want=%v", got, want)
	}
}

func TestAppendOnlyGuardTriggerNames(t *testing.T) {
	want := []string{
		"research_artifact_version_immutable_guard",
		"research_artifact_policy_mutation_append_only_guard",
		"research_artifact_lifecycle_event_append_only_guard",
	}
	got := AppendOnlyGuardTriggerNames()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("append-only guard triggers=%v want=%v", got, want)
	}
}

func TestMigrationDiagnosticReasonCodes(t *testing.T) {
	want := []string{
		"ambiguous_local_key",
		"cross_scope_reference",
		"cyclic_local_reference",
		"dangling_local_key",
		"duplicate_local_key",
		"invalid_match_decision",
		"malformed_uuid",
		"mismatched_reference",
		"unknown_schema",
		"unresolved_reference",
	}
	got := MigrationDiagnosticReasonCodes()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("migration diagnostic reasons=%v want=%v", got, want)
	}
}

func TestMigrationRelationshipParserNames(t *testing.T) {
	want := []string{
		"research_claim_method_evidence_standard",
		"research_message_match_decision",
		"research_message_sender_principal",
		"research_decision_inputs",
		"research_decision_evaluation_local_references",
		"research_graph_node_payload",
		"research_legacy_source_payload",
		"research_report_structured",
		"research_run_event_payload",
		"research_task_remediation_acceptance_criteria",
	}
	got := MigrationRelationshipParserNames()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("migration relationship parsers=%v want=%v", got, want)
	}
}

func TestScopedRelationshipFKNames(t *testing.T) {
	want := []string{
		"research_message_run_event_scoped_fkey",
		"research_message_target_agent_scoped_fkey",
		"research_task_attempt_task_scoped_fkey",
		"research_task_question_scoped_fkey",
		"research_task_parent_task_scoped_fkey",
		"research_question_parent_question_scoped_fkey",
		"research_question_created_by_task_scoped_fkey",
		"research_question_answer_claim_scoped_fkey",
		"research_task_dependency_session_fkey",
		"research_task_dependency_task_scoped_fkey",
		"research_task_dependency_depends_on_scoped_fkey",
		"research_source_snapshot_produced_by_task_scoped_fkey",
		"research_observation_source_snapshot_scoped_fkey",
		"research_observation_produced_by_task_scoped_fkey",
		"research_claim_produced_by_task_scoped_fkey",
		"research_claim_evidence_claim_scoped_fkey",
		"research_claim_evidence_observation_scoped_fkey",
		"research_claim_evidence_verified_by_task_scoped_fkey",
		"research_source_source_snapshot_scoped_fkey",
		"research_report_claim_report_scoped_fkey",
		"research_report_claim_claim_scoped_fkey",
		"research_graph_edge_from_node_scoped_fkey",
		"research_graph_edge_to_node_scoped_fkey",
	}
	got := ScopedRelationshipFKNames()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("scoped relationship fks=%v want=%v", got, want)
	}
}

func TestCanonicalizationRegistryConstraintNames(t *testing.T) {
	want := []string{"research_artifact_version_schema_family_check"}
	got := CanonicalizationRegistryConstraintNames()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("canonicalization registry constraints=%v want=%v", got, want)
	}
}
