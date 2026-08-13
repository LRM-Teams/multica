ALTER TABLE research_artifact_context_manifest
  ADD COLUMN gate_snapshot_bytes BYTEA NOT NULL DEFAULT ''::bytea,
  ADD COLUMN gate_snapshot_hash TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN research_artifact_context_manifest.principal_header_bytes IS
  'Frozen bounded Fleet principal metadata used to render the dispatch prompt.';
COMMENT ON COLUMN research_artifact_context_manifest.gate_snapshot_bytes IS
  'Frozen delivery-gate projection for task-bound reads; not principal metadata.';
