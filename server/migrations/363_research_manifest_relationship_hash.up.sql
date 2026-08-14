-- Chapter D §15.8: bind exact input-reference identity and ordering, not only
-- aggregate counts, into every newly assembled Manifest entry.

ALTER TABLE research_artifact_context_entry
  ADD COLUMN selection_relationship_hash TEXT;

ALTER TABLE research_artifact_context_entry
  ADD CONSTRAINT research_artifact_context_entry_selection_relationship_hash_check
  CHECK (
    selection_relationship_hash IS NULL
    OR selection_relationship_hash ~ '^sha256:[0-9a-f]{64}$'
  );

COMMENT ON COLUMN research_artifact_context_entry.selection_relationship_hash IS
  'Canonical hash of exact current-version input/output reference IDs, endpoints, relation, manifest, use flag, purpose, and ordinal. NULL only for manifests created before migration 363.';
