-- Chapter D §15.8: omissions are part of the frozen dispatch decision, not
-- mutable diagnostics. Seal existing and future omission rows with a digest on
-- the owning Manifest so Result acceptance can reject any rewritten decision.

ALTER TABLE research_artifact_context_manifest
  ADD COLUMN omission_hash TEXT NOT NULL DEFAULT '';

UPDATE research_artifact_context_manifest manifest
SET omission_hash = 'sha256:' || encode(sha256(convert_to(COALESCE((
  SELECT string_agg(
    format('omission=%s:%s:%s', omission.ordinal, omission.candidate_version_id, omission.reason),
    E'\n' ORDER BY omission.ordinal
  )
  FROM research_artifact_context_omission omission
  WHERE (omission.workspace_id, omission.session_id, omission.manifest_id) =
        (manifest.workspace_id, manifest.session_id, manifest.id)
), ''), 'UTF8')), 'hex');

ALTER TABLE research_artifact_context_manifest
  ADD CONSTRAINT research_artifact_context_manifest_omission_hash_check
  CHECK (omission_hash ~ '^sha256:[0-9a-f]{64}$');
