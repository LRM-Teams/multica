DROP INDEX IF EXISTS idx_memory_curation_run_scheduled_profile_date;
DROP INDEX IF EXISTS idx_memory_curation_run_runtime_queue;
DROP INDEX IF EXISTS idx_memory_curator_profile_schedule;
DROP INDEX IF EXISTS idx_memory_curator_profile_runtime;

ALTER TABLE memory_curation_run
  DROP CONSTRAINT IF EXISTS memory_curation_run_status_check,
  ADD CONSTRAINT memory_curation_run_status_check
    CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
  DROP COLUMN IF EXISTS claim_token,
  DROP COLUMN IF EXISTS claimed_at,
  DROP COLUMN IF EXISTS attempt,
  DROP COLUMN IF EXISTS execution_owner,
  DROP COLUMN IF EXISTS target_agent_ids,
  DROP COLUMN IF EXISTS config_version,
  DROP COLUMN IF EXISTS confidence_threshold,
  DROP COLUMN IF EXISTS curator_mode,
  DROP COLUMN IF EXISTS curator_model,
  DROP COLUMN IF EXISTS curator_agent_id,
  DROP COLUMN IF EXISTS runtime_id,
  DROP COLUMN IF EXISTS owner_user_id,
  DROP COLUMN IF EXISTS profile_id;

DROP TABLE IF EXISTS memory_curator_target;
DROP TABLE IF EXISTS memory_curator_profile;
