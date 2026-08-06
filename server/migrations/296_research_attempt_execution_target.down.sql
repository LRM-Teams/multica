DROP TRIGGER IF EXISTS research_task_attempt_execution_target_immutable_guard
  ON research_task_attempt;
DROP FUNCTION IF EXISTS research_attempt_execution_target_immutable();

DROP INDEX IF EXISTS research_task_attempt_execution_target_idx;

-- The migration never changes the outbox request_version contract. Recreate
-- the original named constraint so rollback also repairs an interrupted or
-- manually altered development schema.
ALTER TABLE research_dispatch_outbox
  DROP CONSTRAINT IF EXISTS research_dispatch_outbox_request_version_check,
  ADD CONSTRAINT research_dispatch_outbox_request_version_check
    CHECK (request_version = 1);

ALTER TABLE research_task_attempt
  DROP CONSTRAINT IF EXISTS research_task_attempt_execution_adapter_check,
  DROP COLUMN IF EXISTS source_failure_reason,
  DROP COLUMN IF EXISTS target_config_fingerprint,
  DROP COLUMN IF EXISTS model,
  DROP COLUMN IF EXISTS provider,
  DROP COLUMN IF EXISTS runtime_id,
  DROP COLUMN IF EXISTS execution_adapter;
