-- Bind successful D-enabled Result replay to the exact authorization and
-- lineage facts that were accepted, not only to caller-controlled payload IDs.

ALTER TABLE research_result_artifact
  ADD COLUMN acceptance_manifest_id UUID,
  ADD COLUMN acceptance_manifest_hash TEXT,
  ADD COLUMN resolved_input_versions JSONB,
  ADD COLUMN acceptance_lineage JSONB,
  ADD CONSTRAINT research_result_artifact_replay_binding_complete_check CHECK (
    (acceptance_manifest_id IS NULL
      AND acceptance_manifest_hash IS NULL
      AND resolved_input_versions IS NULL
      AND acceptance_lineage IS NULL)
    OR
    (acceptance_manifest_id IS NOT NULL
      AND acceptance_manifest_hash IS NOT NULL
      AND research_artifact_content_hash_valid(acceptance_manifest_hash)
      AND resolved_input_versions IS NOT NULL
      AND jsonb_typeof(resolved_input_versions) = 'array'
      AND acceptance_lineage IS NOT NULL
      AND jsonb_typeof(acceptance_lineage) = 'array')
  ),
  ADD CONSTRAINT research_result_artifact_acceptance_manifest_fkey
    FOREIGN KEY (workspace_id, session_id, acceptance_manifest_id)
    REFERENCES research_artifact_context_manifest (workspace_id, session_id, id);

WITH bindings AS (
  SELECT
    result.id AS result_id,
    result.workspace_id,
    result.session_id,
    manifest.id AS manifest_id,
    manifest.manifest_hash,
    COALESCE((
      SELECT jsonb_agg(version_id ORDER BY version_id)
      FROM (
        SELECT DISTINCT reference.input_version_id::text AS version_id
        FROM research_artifact_version result_version
        JOIN research_artifact_input_reference reference
          ON reference.workspace_id = result_version.workspace_id
         AND reference.session_id = result_version.session_id
         AND reference.consumer_version_id = result_version.id
        WHERE result_version.workspace_id = result.workspace_id
          AND result_version.session_id = result.session_id
          AND result_version.artifact_id = result.id
      ) versions
    ), '[]'::jsonb) AS resolved_input_versions,
    COALESCE((
      SELECT jsonb_agg(jsonb_build_object(
        'input_version_id', reference.input_version_id::text,
        'relation', reference.relation,
        'manifest_id', COALESCE(reference.manifest_id::text, ''),
        'explicitly_used', reference.explicitly_used,
        'purpose', reference.purpose,
        'ordinal', reference.ordinal
      ) ORDER BY reference.ordinal, reference.input_version_id::text, reference.relation)
      FROM research_artifact_version result_version
      JOIN research_artifact_input_reference reference
        ON reference.workspace_id = result_version.workspace_id
       AND reference.session_id = result_version.session_id
       AND reference.consumer_version_id = result_version.id
      WHERE result_version.workspace_id = result.workspace_id
        AND result_version.session_id = result.session_id
        AND result_version.artifact_id = result.id
    ), '[]'::jsonb) AS acceptance_lineage
  FROM research_result_artifact result
  JOIN research_artifact_context_manifest manifest
    ON manifest.workspace_id = result.workspace_id
   AND manifest.session_id = result.session_id
   AND manifest.attempt_id = result.attempt_id
  WHERE research_artifact_content_hash_valid(manifest.manifest_hash)
)
UPDATE research_result_artifact result
SET acceptance_manifest_id = binding.manifest_id,
    acceptance_manifest_hash = binding.manifest_hash,
    resolved_input_versions = binding.resolved_input_versions,
    acceptance_lineage = binding.acceptance_lineage
FROM bindings binding
WHERE result.workspace_id = binding.workspace_id
  AND result.session_id = binding.session_id
  AND result.id = binding.result_id;

CREATE OR REPLACE FUNCTION research_result_artifact_replay_binding_immutable_guard()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF OLD.acceptance_manifest_id IS NOT NULL AND (
    NEW.acceptance_manifest_id IS DISTINCT FROM OLD.acceptance_manifest_id
    OR NEW.acceptance_manifest_hash IS DISTINCT FROM OLD.acceptance_manifest_hash
    OR NEW.resolved_input_versions IS DISTINCT FROM OLD.resolved_input_versions
    OR NEW.acceptance_lineage IS DISTINCT FROM OLD.acceptance_lineage
  ) THEN
    RAISE EXCEPTION 'research result replay binding is immutable'
      USING ERRCODE = '55000', CONSTRAINT = 'research_result_artifact_replay_binding_immutable_guard';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_result_artifact_replay_binding_immutable_guard
BEFORE UPDATE OF acceptance_manifest_id, acceptance_manifest_hash, resolved_input_versions, acceptance_lineage
ON research_result_artifact
FOR EACH ROW EXECUTE FUNCTION research_result_artifact_replay_binding_immutable_guard();
