-- Source-agent lineage and expanded env-dispatch binding identity/state for
-- derived-agent provisioning. Backward compatible: legacy bindings keep
-- agent_id as the addressed source agent and source_agent_id NULL; new
-- derived-agent bindings populate source_agent_id (= agent_id, the source
-- agent) plus derived_agent_id once the derived global agent is created.
-- Legacy status names ('provisioning', 'failed') are retained so old rows
-- remain valid during feature-gated rollout.

ALTER TABLE agent ADD COLUMN IF NOT EXISTS source_agent_id uuid;
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_source_workspace_fk;
ALTER TABLE agent ADD CONSTRAINT agent_source_workspace_fk
  FOREIGN KEY (workspace_id, source_agent_id)
  REFERENCES agent(workspace_id, id) ON DELETE RESTRICT;

ALTER TABLE environment_agent_sandbox
  ADD COLUMN IF NOT EXISTS id uuid NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN IF NOT EXISTS source_agent_id uuid,
  ADD COLUMN IF NOT EXISTS derived_agent_id uuid,
  ADD COLUMN IF NOT EXISTS training_session_id text,
  ADD COLUMN IF NOT EXISTS training_session_ref text,
  ADD COLUMN IF NOT EXISTS training_session_key text,
  ADD COLUMN IF NOT EXISTS credential_kind text,
  ADD COLUMN IF NOT EXISTS model_config_owner_agent_id uuid;

CREATE UNIQUE INDEX IF NOT EXISTS environment_agent_sandbox_id_uidx
  ON environment_agent_sandbox(id);
CREATE UNIQUE INDEX IF NOT EXISTS environment_agent_sandbox_source_uidx
  ON environment_agent_sandbox(env_id, source_agent_id)
  WHERE source_agent_id IS NOT NULL;

ALTER TABLE environment_agent_sandbox
  DROP CONSTRAINT IF EXISTS environment_agent_sandbox_status_check,
  ADD CONSTRAINT environment_agent_sandbox_status_check CHECK (status IN (
    'pending', 'provisioning', 'failed', 'credential_ready', 'sandbox_creating',
    'runtime_waiting', 'agent_creating', 'ready', 'failed_retryable',
    'deleting', 'deleted'
  ));
