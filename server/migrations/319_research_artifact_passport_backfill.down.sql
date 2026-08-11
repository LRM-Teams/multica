-- Roll back D1b legacy artifact passport backfill helpers and migration-recomputed rows.

DROP TRIGGER IF EXISTS research_artifact_version_immutable_guard ON research_artifact_version;

DELETE FROM research_artifact_version
WHERE hash_origin = 'migration_recomputed';

DELETE FROM research_artifact_passport p
WHERE p.current_version IS NULL
   OR NOT EXISTS (
     SELECT 1 FROM research_artifact_version v
     WHERE v.workspace_id = p.workspace_id
       AND v.session_id = p.session_id
       AND v.artifact_id = p.id
   );

CREATE TRIGGER research_artifact_version_immutable_guard
BEFORE UPDATE OR DELETE ON research_artifact_version
FOR EACH ROW EXECUTE FUNCTION research_artifact_version_immutable_guard();

DROP FUNCTION IF EXISTS research_artifact_backfill_registered(UUID, UUID, UUID, TEXT, TIMESTAMPTZ, INTEGER, INTEGER);
DROP FUNCTION IF EXISTS research_artifact_migration_content_hash(TEXT, UUID, UUID, UUID);
