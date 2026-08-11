DROP INDEX IF EXISTS agent_activity_launch_dispatch_idx;

ALTER TABLE agent_activity_launch
  DROP COLUMN IF EXISTS runtime_generation,
  DROP COLUMN IF EXISTS provider_turn_id,
  DROP COLUMN IF EXISTS provider_session_id,
  DROP COLUMN IF EXISTS accepted_at,
  DROP COLUMN IF EXISTS queue_age_ms,
  DROP COLUMN IF EXISTS queue_depth,
  DROP COLUMN IF EXISTS queue_state,
  DROP COLUMN IF EXISTS start_dispatch_id;

ALTER TABLE agent_activity_launch
  DROP CONSTRAINT agent_activity_launch_status_check;

ALTER TABLE agent_activity_launch
  ADD CONSTRAINT agent_activity_launch_status_check
  CHECK (status IN ('active', 'inactive'));
