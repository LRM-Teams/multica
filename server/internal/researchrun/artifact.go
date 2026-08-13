package researchrun

import "fmt"

// ArtifactEntityKind identifies one canonical Research artifact passport.
type ArtifactEntityKind string

const (
	ArtifactKindRunSession           ArtifactEntityKind = "run_session"
	ArtifactKindContractRevision     ArtifactEntityKind = "contract_revision"
	ArtifactKindMethodDecision       ArtifactEntityKind = "method_decision"
	ArtifactKindQuestion             ArtifactEntityKind = "question"
	ArtifactKindTask                 ArtifactEntityKind = "task"
	ArtifactKindAttempt              ArtifactEntityKind = "attempt"
	ArtifactKindResultArtifact       ArtifactEntityKind = "result_artifact"
	ArtifactKindLegacySource         ArtifactEntityKind = "legacy_source"
	ArtifactKindSourceSnapshot       ArtifactEntityKind = "source_snapshot"
	ArtifactKindObservation          ArtifactEntityKind = "observation"
	ArtifactKindClaim                ArtifactEntityKind = "claim"
	ArtifactKindEvidenceLink         ArtifactEntityKind = "evidence_link"
	ArtifactKindReportRevision       ArtifactEntityKind = "report_revision"
	ArtifactKindEvaluationDecision   ArtifactEntityKind = "evaluation_decision"
	ArtifactKindStageEvaluation      ArtifactEntityKind = "stage_evaluation"
	ArtifactKindResearchMessage      ArtifactEntityKind = "research_message"
	ArtifactKindProductRoundDecision ArtifactEntityKind = "product_round_decision"
	ArtifactKindContextManifest      ArtifactEntityKind = "context_manifest"
	ArtifactKindRunEvent             ArtifactEntityKind = "run_event"
	ArtifactKindGraphNode            ArtifactEntityKind = "graph_node"
	ArtifactKindGraphEdge            ArtifactEntityKind = "graph_edge"
	ArtifactKindHypothesis           ArtifactEntityKind = "hypothesis"
	ArtifactKindBranch               ArtifactEntityKind = "branch"
	ArtifactKindInsight              ArtifactEntityKind = "insight"
	ArtifactKindInquiryEdge          ArtifactEntityKind = "inquiry_edge"
	ArtifactKindSearchPlan           ArtifactEntityKind = "search_plan"
	ArtifactKindQueryExecution       ArtifactEntityKind = "query_execution"
	ArtifactKindSourceCandidate      ArtifactEntityKind = "source_candidate"
	ArtifactKindScreeningDecision    ArtifactEntityKind = "screening_decision"
)

var registeredArtifactEntityKinds = map[ArtifactEntityKind]struct{}{
	ArtifactKindRunSession:           {},
	ArtifactKindContractRevision:     {},
	ArtifactKindMethodDecision:       {},
	ArtifactKindQuestion:             {},
	ArtifactKindTask:                 {},
	ArtifactKindAttempt:              {},
	ArtifactKindResultArtifact:       {},
	ArtifactKindLegacySource:         {},
	ArtifactKindSourceSnapshot:       {},
	ArtifactKindObservation:          {},
	ArtifactKindClaim:                {},
	ArtifactKindEvidenceLink:         {},
	ArtifactKindReportRevision:       {},
	ArtifactKindEvaluationDecision:   {},
	ArtifactKindStageEvaluation:      {},
	ArtifactKindResearchMessage:      {},
	ArtifactKindProductRoundDecision: {},
	ArtifactKindContextManifest:      {},
	ArtifactKindRunEvent:             {},
	ArtifactKindGraphNode:            {},
	ArtifactKindGraphEdge:            {},
	ArtifactKindHypothesis:           {},
	ArtifactKindBranch:               {},
	ArtifactKindInsight:              {},
	ArtifactKindInquiryEdge:          {},
	ArtifactKindSearchPlan:           {},
	ArtifactKindQueryExecution:       {},
	ArtifactKindSourceCandidate:      {},
	ArtifactKindScreeningDecision:    {},
}

// ArtifactLifecycleStatus is passport admissibility, not domain status.
type ArtifactLifecycleStatus string

const (
	ArtifactLifecycleRegistered ArtifactLifecycleStatus = "registered"
	ArtifactLifecycleAccepted   ArtifactLifecycleStatus = "accepted"
	ArtifactLifecycleRejected   ArtifactLifecycleStatus = "rejected"
	ArtifactLifecycleStale      ArtifactLifecycleStatus = "stale"
	ArtifactLifecycleSuperseded ArtifactLifecycleStatus = "superseded"
	ArtifactLifecycleWithdrawn  ArtifactLifecycleStatus = "withdrawn"
)

// ArtifactProvenanceCompleteness records how much producer history storage proves.
type ArtifactProvenanceCompleteness string

const (
	ArtifactProvenanceComplete ArtifactProvenanceCompleteness = "complete"
	ArtifactProvenancePartial  ArtifactProvenanceCompleteness = "partial"
	ArtifactProvenanceUnknown  ArtifactProvenanceCompleteness = "unknown"
)

// ArtifactAccessLevel is a policy profile for ordinary Research execution.
type ArtifactAccessLevel string

const (
	ArtifactAccessVerifiedOnly ArtifactAccessLevel = "verified_only"
	ArtifactAccessRedacted     ArtifactAccessLevel = "redacted"
	ArtifactAccessRaw          ArtifactAccessLevel = "raw"
)

// ArtifactHashOrigin records how a version content hash was produced.
type ArtifactHashOrigin string

const (
	ArtifactHashOriginProduction          ArtifactHashOrigin = "production"
	ArtifactHashOriginMigrationRecomputed ArtifactHashOrigin = "migration_recomputed"
	ArtifactHashOriginLegacyStored        ArtifactHashOrigin = "legacy_stored"
)

// ArtifactCanonicalizationVersion is the active canonical JSON profile.
const ArtifactCanonicalizationVersion = "research-artifact-c14n-v1"

// LegacyV1V5CompatPolicy is the named ordinary-task admission exception for backfilled rows.
const LegacyV1V5CompatPolicy = "legacy-v1-v5-compat-v1"

func ParseArtifactEntityKind(raw string) (ArtifactEntityKind, error) {
	kind := ArtifactEntityKind(raw)
	if _, ok := registeredArtifactEntityKinds[kind]; !ok {
		return "", fmt.Errorf("%w: unknown artifact entity kind %q", ErrInvalidContract, raw)
	}
	return kind, nil
}

func RegisteredArtifactEntityKinds() []ArtifactEntityKind {
	out := make([]ArtifactEntityKind, 0, len(registeredArtifactEntityKinds))
	for kind := range registeredArtifactEntityKinds {
		out = append(out, kind)
	}
	return out
}

// ReciprocalArtifactPassportGuardTriggerNames lists migration 320 deferred insert guards.
func ReciprocalArtifactPassportGuardTriggerNames() []string {
	return []string{
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
}

// PolicyCouplingGuardTriggerNames lists migration 321 verification/grant policy guards.
func PolicyCouplingGuardTriggerNames() []string {
	return []string{
		"research_source_snapshot_verification_to_policy_guard",
		"research_observation_verification_to_policy_guard",
		"research_claim_evidence_verification_to_policy_guard",
		"research_artifact_policy_mutation_to_verification_guard",
		"research_artifact_policy_grant_to_mutation_guard",
		"research_artifact_policy_mutation_to_grant_guard",
	}
}

// PolicyLedgerGuardTriggerNames lists migration 322 generic policy ledger guards.
func PolicyLedgerGuardTriggerNames() []string {
	return []string{
		"research_artifact_passport_to_policy_mutation_guard",
		"research_artifact_policy_mutation_to_passport_guard",
	}
}

// IntegrityGuardTriggerNames lists migration 323 producer/projection guards.
func IntegrityGuardTriggerNames() []string {
	return []string{
		"research_artifact_version_producer_guard",
		"research_result_attempt_projection_guard",
	}
}

// LinkPolicyGuardTriggerNames lists migration 324 supersession/lifecycle policy guards.
func LinkPolicyGuardTriggerNames() []string {
	return []string{
		"research_artifact_supersession_to_policy_guard",
		"research_artifact_policy_mutation_to_supersession_guard",
		"research_artifact_lifecycle_event_to_policy_guard",
		"research_artifact_policy_mutation_to_lifecycle_event_guard",
	}
}

// MigrationDiagnosticReasonCodes lists migration 325 diagnostic reason registry.
func MigrationDiagnosticReasonCodes() []string {
	return []string{
		"cross_scope_reference",
		"invalid_match_decision",
		"malformed_uuid",
		"unknown_schema",
		"unresolved_reference",
	}
}

// MigrationRelationshipParserNames lists migration 325 relationship parser registry.
func MigrationRelationshipParserNames() []string {
	return []string{
		"research_message_match_decision",
		"research_decision_inputs",
		"research_run_event_payload",
	}
}

// ScopedRelationshipFKNames lists migration 326 composite relationship FKs.
func ScopedRelationshipFKNames() []string {
	return []string{
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
}

// CanonicalizationRegistryConstraintNames lists migration 327 schema-family checks.
func CanonicalizationRegistryConstraintNames() []string {
	return []string{
		"research_artifact_version_schema_family_check",
	}
}
