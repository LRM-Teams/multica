DROP TRIGGER IF EXISTS research_result_artifact_replay_binding_immutable_guard
  ON research_result_artifact;
DROP FUNCTION IF EXISTS research_result_artifact_replay_binding_immutable_guard();

ALTER TABLE research_result_artifact
  DROP CONSTRAINT IF EXISTS research_result_artifact_acceptance_manifest_fkey,
  DROP CONSTRAINT IF EXISTS research_result_artifact_replay_binding_complete_check,
  DROP COLUMN IF EXISTS acceptance_lineage,
  DROP COLUMN IF EXISTS resolved_input_versions,
  DROP COLUMN IF EXISTS acceptance_manifest_hash,
  DROP COLUMN IF EXISTS acceptance_manifest_id;
