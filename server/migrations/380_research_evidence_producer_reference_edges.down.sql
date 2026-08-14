-- Input-reference history is append-only; preserve materialized lineage.
DROP FUNCTION IF EXISTS research_artifact_scan_session_migration_diagnostics(UUID,UUID);
ALTER FUNCTION research_artifact_scan_session_migration_diagnostics_v379(UUID,UUID)
  RENAME TO research_artifact_scan_session_migration_diagnostics;
DROP FUNCTION IF EXISTS research_artifact_materialize_observation_complete_references(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_materialize_observation_producer(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_materialize_source_snapshot_producer(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_insert_evidence_producer_reference(UUID,UUID,TEXT,UUID,UUID,TEXT,TEXT);
