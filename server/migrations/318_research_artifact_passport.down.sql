-- Roll back Chapter D1 artifact passport foundation.

DROP TRIGGER IF EXISTS research_artifact_passport_class_guard ON research_artifact_passport;
DROP FUNCTION IF EXISTS research_artifact_passport_class_guard_fn();

DROP TRIGGER IF EXISTS research_artifact_lifecycle_event_append_only_guard ON research_artifact_lifecycle_event;
DROP FUNCTION IF EXISTS research_artifact_lifecycle_event_append_only_guard();

DROP TRIGGER IF EXISTS research_artifact_policy_mutation_append_only_guard ON research_artifact_policy_mutation;
DROP FUNCTION IF EXISTS research_artifact_policy_mutation_append_only_guard();

DROP TRIGGER IF EXISTS research_artifact_version_immutable_guard ON research_artifact_version;
DROP FUNCTION IF EXISTS research_artifact_version_immutable_guard();

DROP TABLE IF EXISTS research_artifact_migration_diagnostic;
DROP TABLE IF EXISTS research_artifact_lifecycle_event;
DROP TABLE IF EXISTS research_artifact_supersession;
DROP TABLE IF EXISTS research_artifact_input_reference;
DROP TABLE IF EXISTS research_artifact_context_omission;
DROP TABLE IF EXISTS research_artifact_context_entry;
DROP TABLE IF EXISTS research_artifact_context_manifest;
DROP TABLE IF EXISTS research_result_artifact;
DROP TABLE IF EXISTS research_artifact_policy_mutation;
DROP TABLE IF EXISTS research_artifact_policy_grant;
DROP TABLE IF EXISTS research_artifact_policy_state;

ALTER TABLE research_artifact_passport
  DROP CONSTRAINT IF EXISTS research_artifact_passport_current_version_fkey;

DROP TABLE IF EXISTS research_artifact_version;
DROP TABLE IF EXISTS research_artifact_passport;

DROP INDEX IF EXISTS research_run_event_scoped_uidx;
DROP INDEX IF EXISTS research_claim_evidence_scoped_uidx;
DROP INDEX IF EXISTS research_graph_edge_scoped_uidx;
DROP INDEX IF EXISTS research_graph_node_scoped_uidx;
DROP INDEX IF EXISTS research_source_scoped_uidx;
DROP INDEX IF EXISTS research_product_round_card_scoped_uidx;
DROP INDEX IF EXISTS research_message_scoped_uidx;
DROP INDEX IF EXISTS research_stage_eval_scoped_uidx;
DROP INDEX IF EXISTS research_decision_scoped_uidx;
DROP INDEX IF EXISTS research_report_scoped_uidx;
DROP INDEX IF EXISTS research_claim_scoped_uidx;
DROP INDEX IF EXISTS research_observation_scoped_uidx;
DROP INDEX IF EXISTS research_source_snapshot_scoped_uidx;
DROP INDEX IF EXISTS research_task_attempt_scoped_uidx;
DROP INDEX IF EXISTS research_task_scoped_uidx;
DROP INDEX IF EXISTS research_question_scoped_uidx;
DROP INDEX IF EXISTS research_contract_revision_scoped_uidx;
DROP INDEX IF EXISTS research_session_workspace_id_uidx;

DROP FUNCTION IF EXISTS research_artifact_context_omission_reason_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_context_representation_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_mutation_kind_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_hash_origin_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_access_level_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_provenance_completeness_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_lifecycle_status_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_entity_kind_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_content_hash_valid(TEXT);
