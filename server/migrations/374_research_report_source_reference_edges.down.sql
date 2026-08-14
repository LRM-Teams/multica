DELETE FROM research_artifact_input_reference
WHERE relation='report_source' AND purpose='report_source_migration';
DROP FUNCTION IF EXISTS research_artifact_materialize_report_source_references(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_insert_report_source_reference(UUID,UUID,UUID,UUID,TEXT,INTEGER);
