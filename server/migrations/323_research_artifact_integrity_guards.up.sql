-- Chapter D1f: version producer and result projection integrity guards (design §4.7.4–5).

CREATE OR REPLACE FUNCTION research_artifact_version_producer_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_attempt research_task_attempt%ROWTYPE;
BEGIN
  IF NEW.produced_by_attempt_id IS NULL OR NEW.produced_by_task_id IS NULL THEN
    RETURN NEW;
  END IF;

  SELECT * INTO v_attempt
  FROM research_task_attempt a
  WHERE a.workspace_id = NEW.workspace_id
    AND a.session_id = NEW.session_id
    AND a.id = NEW.produced_by_attempt_id;

  IF NOT FOUND OR v_attempt.task_id IS DISTINCT FROM NEW.produced_by_task_id THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_version_producer_guard';
  END IF;

  IF NEW.produced_by_agent_id IS NOT NULL
     AND NEW.produced_by_agent_id IS DISTINCT FROM v_attempt.assigned_agent_id THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_version_producer_guard';
  END IF;

  IF btrim(NEW.model) <> '' AND NEW.model IS DISTINCT FROM v_attempt.model THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_version_producer_guard';
  END IF;
  IF btrim(NEW.provider) <> '' AND NEW.provider IS DISTINCT FROM v_attempt.provider THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_version_producer_guard';
  END IF;
  IF btrim(NEW.execution_adapter) <> ''
     AND NEW.execution_adapter IS DISTINCT FROM v_attempt.execution_adapter THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_version_producer_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_version_producer_guard
AFTER INSERT OR UPDATE OF produced_by_task_id, produced_by_attempt_id, produced_by_agent_id,
  model, provider, execution_adapter
ON research_artifact_version
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_version_producer_guard_fn();

CREATE OR REPLACE FUNCTION research_result_attempt_projection_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_attempt research_task_attempt%ROWTYPE;
BEGIN
  SELECT * INTO v_attempt
  FROM research_task_attempt a
  WHERE a.workspace_id = NEW.workspace_id
    AND a.session_id = NEW.session_id
    AND a.id = NEW.attempt_id;

  IF NOT FOUND THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_result_attempt_projection_guard';
  END IF;

  IF v_attempt.result_hash IS NULL OR btrim(v_attempt.result_hash) = '' THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_result_attempt_projection_guard';
  END IF;
  IF NEW.content_hash IS DISTINCT FROM v_attempt.result_hash THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_result_attempt_projection_guard';
  END IF;
  IF NEW.client_request_id IS DISTINCT FROM COALESCE(v_attempt.client_request_id, '') THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_result_attempt_projection_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_result_attempt_projection_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, attempt_id, content_hash, client_request_id
ON research_result_artifact
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_result_attempt_projection_guard_fn();
