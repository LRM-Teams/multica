-- Input-reference history is append-only; preserve materialized lineage.
DROP FUNCTION IF EXISTS research_artifact_scan_session_migration_diagnostics(UUID,UUID);
ALTER FUNCTION research_artifact_scan_session_migration_diagnostics_v382(UUID,UUID)
  RENAME TO research_artifact_scan_session_migration_diagnostics;
DROP FUNCTION IF EXISTS research_artifact_materialize_evaluation_local_references(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_materialize_evaluation_local_references_if_clean(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_materialize_evaluation_claim_key_array(UUID,UUID,UUID,UUID,JSONB,TEXT,TEXT);
DROP FUNCTION IF EXISTS research_artifact_insert_evaluation_claim_key_reference(UUID,UUID,UUID,UUID,TEXT,TEXT,TEXT);
