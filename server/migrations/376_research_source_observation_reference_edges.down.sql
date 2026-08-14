-- Input-reference history is append-only; preserve materialized lineage.
DROP FUNCTION IF EXISTS research_artifact_materialize_observation_reference(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_materialize_legacy_source_reference(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_insert_source_snapshot_reference(UUID,UUID,TEXT,UUID,UUID,TEXT,TEXT,TEXT);
