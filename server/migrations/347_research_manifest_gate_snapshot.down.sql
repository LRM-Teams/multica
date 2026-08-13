ALTER TABLE research_artifact_context_manifest
  DROP COLUMN IF EXISTS gate_snapshot_hash,
  DROP COLUMN IF EXISTS gate_snapshot_bytes;
