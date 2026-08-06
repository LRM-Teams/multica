-- Freeze the concrete execution target on each Research Attempt. Circuit and
-- repair decisions must be attributable to the target that actually received
-- the immutable outbox request, not whatever configuration the Agent has now.

ALTER TABLE research_task_attempt
  ADD COLUMN execution_adapter TEXT NOT NULL DEFAULT 'agent_inbox',
  ADD COLUMN runtime_id UUID,
  ADD COLUMN provider TEXT NOT NULL DEFAULT '',
  ADD COLUMN model TEXT NOT NULL DEFAULT '',
  ADD COLUMN target_config_fingerprint TEXT NOT NULL DEFAULT '',
  ADD COLUMN source_failure_reason TEXT NOT NULL DEFAULT '',
  ADD CONSTRAINT research_task_attempt_execution_adapter_check
    CHECK (btrim(execution_adapter) <> '');

UPDATE research_task_attempt attempt
SET runtime_id = agent.runtime_id,
    provider = COALESCE(runtime.provider, ''),
    model = COALESCE(agent.model, '')
FROM agent
LEFT JOIN agent_runtime runtime ON runtime.id = agent.runtime_id
WHERE attempt.assigned_agent_id = agent.id;

CREATE INDEX research_task_attempt_execution_target_idx
  ON research_task_attempt (workspace_id, execution_adapter, provider, status, created_at);

CREATE OR REPLACE FUNCTION research_attempt_execution_target_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.execution_adapter IS DISTINCT FROM OLD.execution_adapter
     OR NEW.runtime_id IS DISTINCT FROM OLD.runtime_id
     OR NEW.provider IS DISTINCT FROM OLD.provider
     OR NEW.model IS DISTINCT FROM OLD.model
     OR NEW.target_config_fingerprint IS DISTINCT FROM OLD.target_config_fingerprint THEN
    RAISE EXCEPTION 'research attempt execution target is immutable'
      USING ERRCODE = '23514', CONSTRAINT = 'research_task_attempt_execution_target_immutable_check';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER research_task_attempt_execution_target_immutable_guard
BEFORE UPDATE OF execution_adapter, runtime_id, provider, model, target_config_fingerprint
ON research_task_attempt
FOR EACH ROW EXECUTE FUNCTION research_attempt_execution_target_immutable();
