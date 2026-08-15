-- Input-reference history is append-only; preserve materialized lineage.
DROP FUNCTION IF EXISTS research_artifact_materialize_task_references(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_insert_task_reference(UUID,UUID,UUID,UUID,TEXT,TEXT,TEXT,INTEGER,TEXT);
