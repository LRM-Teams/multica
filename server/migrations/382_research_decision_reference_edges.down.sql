-- Input-reference history is append-only; preserve materialized lineage.
DROP FUNCTION IF EXISTS research_artifact_scan_session_migration_diagnostics(UUID,UUID);
ALTER FUNCTION research_artifact_scan_session_migration_diagnostics_v381(UUID,UUID)
  RENAME TO research_artifact_scan_session_migration_diagnostics;
DROP FUNCTION IF EXISTS research_artifact_materialize_decision_references(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_materialize_decision_references_if_clean(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_materialize_decision_array_references(UUID,UUID,UUID,TEXT,JSONB,TEXT,TEXT,TEXT);
DROP FUNCTION IF EXISTS research_artifact_insert_decision_reference(UUID,UUID,UUID,TEXT,UUID,TEXT,TEXT,INTEGER,TEXT);
