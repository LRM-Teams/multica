-- Chapter D completion (D2): bind dispatch outbox to frozen manifest identity.

ALTER TABLE research_dispatch_outbox
  ADD COLUMN IF NOT EXISTS manifest_id UUID,
  ADD COLUMN IF NOT EXISTS manifest_hash TEXT NOT NULL DEFAULT '';

ALTER TABLE research_dispatch_outbox
  ADD CONSTRAINT research_dispatch_outbox_manifest_hash_check
  CHECK (manifest_hash = '' OR manifest_hash ~ '^sha256:[0-9a-f]{64}$');

ALTER TABLE research_dispatch_outbox
  ADD CONSTRAINT research_dispatch_outbox_manifest_fkey
  FOREIGN KEY (workspace_id, session_id, manifest_id)
  REFERENCES research_artifact_context_manifest (workspace_id, session_id, id);
