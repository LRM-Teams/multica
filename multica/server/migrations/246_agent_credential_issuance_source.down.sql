DROP INDEX IF EXISTS idx_agent_credential_daemon_subject_unrevoked;

ALTER TABLE agent_credential
  DROP CONSTRAINT IF EXISTS agent_credential_issuance_source_check;

ALTER TABLE agent_credential
  DROP COLUMN IF EXISTS issuance_source;
