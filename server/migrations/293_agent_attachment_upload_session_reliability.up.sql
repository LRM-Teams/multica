ALTER TABLE agent_attachment_upload_session
  ADD COLUMN checksum_sha256 TEXT,
  ADD COLUMN idempotency_key UUID,
  ADD COLUMN cancelled_at TIMESTAMPTZ,
  ADD COLUMN failure_code TEXT;

ALTER TABLE agent_attachment_upload_session
  DROP CONSTRAINT agent_attachment_upload_session_state_check;

ALTER TABLE agent_attachment_upload_session
  ADD CONSTRAINT agent_attachment_upload_session_state_check
  CHECK (state IN ('pending', 'completed', 'cancelled'));

ALTER TABLE agent_attachment_upload_session
  DROP CONSTRAINT agent_attachment_upload_session_check1;

ALTER TABLE agent_attachment_upload_session
  ADD CONSTRAINT agent_attachment_upload_session_check1
  CHECK (
    (state = 'pending' AND attachment_id IS NULL AND completed_at IS NULL AND cancelled_at IS NULL)
    OR
    (state = 'completed' AND attachment_id IS NOT NULL AND completed_at IS NOT NULL AND cancelled_at IS NULL)
    OR
    (state = 'cancelled' AND attachment_id IS NULL AND completed_at IS NULL AND cancelled_at IS NOT NULL)
  );

CREATE UNIQUE INDEX idx_agent_attachment_upload_session_idempotency
  ON agent_attachment_upload_session(workspace_id, agent_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
