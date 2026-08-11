-- Roll back Chapter D1d policy coupling guards.

DROP TRIGGER IF EXISTS research_artifact_policy_mutation_to_grant_guard ON research_artifact_policy_mutation;
DROP FUNCTION IF EXISTS research_artifact_policy_mutation_to_grant_guard_fn();

DROP TRIGGER IF EXISTS research_artifact_policy_grant_to_mutation_guard ON research_artifact_policy_grant;
DROP FUNCTION IF EXISTS research_artifact_policy_grant_to_mutation_guard_fn();

DROP TRIGGER IF EXISTS research_artifact_policy_mutation_to_verification_guard ON research_artifact_policy_mutation;
DROP FUNCTION IF EXISTS research_artifact_policy_mutation_to_verification_guard_fn();

DROP TRIGGER IF EXISTS research_claim_evidence_verification_to_policy_guard ON research_claim_evidence;
DROP TRIGGER IF EXISTS research_observation_verification_to_policy_guard ON research_observation;
DROP TRIGGER IF EXISTS research_source_snapshot_verification_to_policy_guard ON research_source_snapshot;
DROP TRIGGER IF EXISTS research_claim_evidence_verification_tx_marker ON research_claim_evidence;
DROP TRIGGER IF EXISTS research_observation_verification_tx_marker ON research_observation;
DROP TRIGGER IF EXISTS research_source_snapshot_verification_tx_marker ON research_source_snapshot;
DROP FUNCTION IF EXISTS research_artifact_verification_domain_marker_fn();
DROP FUNCTION IF EXISTS research_artifact_verification_domain_to_policy_guard_fn();

DROP FUNCTION IF EXISTS research_artifact_require_grant_policy_coupling(UUID, UUID, UUID, TEXT, BIGINT, BIGINT, TEXT, TEXT, TEXT);
DROP FUNCTION IF EXISTS research_artifact_require_verification_policy_coupling(TEXT, UUID, UUID, UUID, TEXT, TEXT, TEXT);
DROP FUNCTION IF EXISTS research_artifact_reserve_policy_watermark(UUID, UUID);
DROP FUNCTION IF EXISTS research_artifact_current_policy_watermark(UUID, UUID);
DROP TABLE IF EXISTS research_artifact_verification_tx_marker;
