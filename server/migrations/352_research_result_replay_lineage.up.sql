-- Freeze the dispatch lineage that made each accepted Result admissible.

ALTER TABLE research_result_artifact
  ADD COLUMN manifest_id UUID,
  ADD COLUMN manifest_hash TEXT NOT NULL DEFAULT '',
  ADD COLUMN input_version_set_hash TEXT NOT NULL DEFAULT '';

UPDATE research_result_artifact result
SET manifest_id = manifest.id,
    manifest_hash = manifest.manifest_hash,
    input_version_set_hash = 'sha256:' || encode(digest(COALESCE((
      SELECT string_agg(entry.artifact_version_id::text, E'\n' ORDER BY entry.artifact_version_id::text)
      FROM research_artifact_context_entry entry
      WHERE entry.workspace_id=result.workspace_id
        AND entry.session_id=result.session_id
        AND entry.manifest_id=manifest.id
    ), ''), 'sha256'), 'hex')
FROM research_artifact_context_manifest manifest
WHERE manifest.workspace_id=result.workspace_id
  AND manifest.session_id=result.session_id
  AND manifest.attempt_id=result.attempt_id;

ALTER TABLE research_result_artifact
  ADD CONSTRAINT research_result_artifact_manifest_scoped_fkey
    FOREIGN KEY (workspace_id, session_id, manifest_id)
    REFERENCES research_artifact_context_manifest (workspace_id, session_id, id),
  ADD CONSTRAINT research_result_artifact_manifest_hash_check
    CHECK (manifest_hash = '' OR research_artifact_content_hash_valid(manifest_hash)),
  ADD CONSTRAINT research_result_artifact_input_version_set_hash_check
    CHECK (input_version_set_hash = '' OR research_artifact_content_hash_valid(input_version_set_hash));
