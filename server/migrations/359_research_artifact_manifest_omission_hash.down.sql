ALTER TABLE research_artifact_context_manifest
  DROP CONSTRAINT IF EXISTS research_artifact_context_manifest_omission_hash_check;

ALTER TABLE research_artifact_context_manifest
  DROP COLUMN IF EXISTS omission_hash;
