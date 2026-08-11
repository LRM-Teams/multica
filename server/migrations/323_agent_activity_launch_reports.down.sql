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

-- The pre-323 schema cannot represent the accepted-but-not-active launch
-- phase. Preserve the launch row and map that phase to the closest legacy
-- state before restoring the narrower constraint.
UPDATE agent_activity_launch
SET status = 'active'
WHERE status = 'accepted';

ALTER TABLE agent_activity_launch
  ADD CONSTRAINT agent_activity_launch_status_check
  CHECK (status IN ('active', 'inactive'));
