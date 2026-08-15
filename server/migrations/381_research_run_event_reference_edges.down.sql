-- Input-reference history is append-only; preserve materialized lineage.
DROP FUNCTION IF EXISTS research_artifact_scan_session_migration_diagnostics(UUID,UUID);
ALTER FUNCTION research_artifact_scan_session_migration_diagnostics_v380(UUID,UUID)
  RENAME TO research_artifact_scan_session_migration_diagnostics;
DROP FUNCTION IF EXISTS research_artifact_materialize_run_event_references(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_materialize_run_event_references_if_clean(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_insert_run_event_reference(UUID,UUID,UUID,UUID,TEXT,TEXT,TEXT);
