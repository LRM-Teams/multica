DROP TABLE IF EXISTS research_execution_circuit_transition;
DROP TABLE IF EXISTS research_execution_circuit;

DROP TRIGGER IF EXISTS research_task_attempt_execution_target_immutable_guard
  ON research_task_attempt;
DROP FUNCTION IF EXISTS research_attempt_execution_target_immutable();

ALTER TABLE research_task_attempt
  DROP COLUMN IF EXISTS provider_config_fingerprint,
  DROP COLUMN IF EXISTS runtime_config_fingerprint,
  DROP COLUMN IF EXISTS agent_config_fingerprint;

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
