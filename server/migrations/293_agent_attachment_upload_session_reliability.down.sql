DROP INDEX IF EXISTS idx_agent_attachment_upload_session_idempotency;

ALTER TABLE agent_attachment_upload_session
  DROP CONSTRAINT agent_attachment_upload_session_check1;

ALTER TABLE agent_attachment_upload_session
  ADD CONSTRAINT agent_attachment_upload_session_check1
  CHECK (
    (state = 'pending' AND attachment_id IS NULL AND completed_at IS NULL)
    OR
    (state = 'completed' AND attachment_id IS NOT NULL AND completed_at IS NOT NULL)
  );

ALTER TABLE agent_attachment_upload_session
  DROP CONSTRAINT agent_attachment_upload_session_state_check;

ALTER TABLE agent_attachment_upload_session
  ADD CONSTRAINT agent_attachment_upload_session_state_check
  CHECK (state IN ('pending', 'completed'));

ALTER TABLE agent_attachment_upload_session
  DROP COLUMN failure_code,
  DROP COLUMN cancelled_at,
  DROP COLUMN idempotency_key,
  DROP COLUMN checksum_sha256;
