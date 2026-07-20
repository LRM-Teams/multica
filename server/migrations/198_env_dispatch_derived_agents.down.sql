-- Reverse 198: restore the original environment_agent_sandbox status check,
-- drop the derived-agent binding columns and indexes, and remove agent
-- source-agent lineage. Existing rows keep source_agent_id NULL; new-path
-- inserts always set it, so no legacy binding is silently claimed as a
-- derived workflow on rollback.

ALTER TABLE environment_agent_sandbox
  DROP CONSTRAINT IF EXISTS environment_agent_sandbox_status_check,
  ADD CONSTRAINT environment_agent_sandbox_status_check CHECK (status IN ('pending', 'provisioning', 'ready', 'failed', 'deleting'));

DROP INDEX IF EXISTS environment_agent_sandbox_source_uidx;
DROP INDEX IF EXISTS environment_agent_sandbox_id_uidx;

ALTER TABLE environment_agent_sandbox
  DROP COLUMN IF EXISTS model_config_owner_agent_id,
  DROP COLUMN IF EXISTS credential_kind,
  DROP COLUMN IF EXISTS training_session_key,
  DROP COLUMN IF EXISTS training_session_ref,
  DROP COLUMN IF EXISTS training_session_id,
  DROP COLUMN IF EXISTS derived_agent_id,
  DROP COLUMN IF EXISTS source_agent_id,
  DROP COLUMN IF EXISTS id;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_source_workspace_fk;
ALTER TABLE agent DROP COLUMN IF EXISTS source_agent_id;
