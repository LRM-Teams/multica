ALTER TABLE research_result_artifact
  DROP CONSTRAINT IF EXISTS research_result_artifact_input_version_set_hash_check,
  DROP CONSTRAINT IF EXISTS research_result_artifact_manifest_hash_check,
  DROP CONSTRAINT IF EXISTS research_result_artifact_manifest_scoped_fkey,
  DROP COLUMN IF EXISTS input_version_set_hash,
  DROP COLUMN IF EXISTS manifest_hash,
  DROP COLUMN IF EXISTS manifest_id;
