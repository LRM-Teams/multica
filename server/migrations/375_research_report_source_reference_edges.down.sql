-- Input-reference history is append-only. Downgrade removes executable
-- migration helpers but deliberately preserves already materialized lineage.
DROP FUNCTION IF EXISTS research_artifact_materialize_report_source_references(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_insert_report_source_reference(UUID,UUID,UUID,UUID,TEXT,INTEGER);
