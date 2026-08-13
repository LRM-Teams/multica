DROP TRIGGER IF EXISTS env_dispatch_run_terminal_snapshot_immutable ON env_dispatch_run;
DROP FUNCTION IF EXISTS reject_terminal_env_dispatch_run_snapshot_mutation();
DROP TRIGGER IF EXISTS env_dispatch_run_status_transition_guard ON env_dispatch_run;
DROP FUNCTION IF EXISTS validate_env_dispatch_run_status_transition();
DROP TRIGGER IF EXISTS env_dispatch_run_agent_readiness_guard ON env_dispatch_run_agent;
DROP FUNCTION IF EXISTS validate_env_dispatch_run_agent_readiness();

DROP TABLE IF EXISTS env_dispatch_activity_transition;
DROP TABLE IF EXISTS env_dispatch_run_agent;

ALTER TABLE env_dispatch_run
  DROP CONSTRAINT IF EXISTS env_dispatch_run_terminal_snapshot_check,
  DROP CONSTRAINT IF EXISTS env_dispatch_run_timeout_origin_check,
  DROP CONSTRAINT IF EXISTS env_dispatch_run_quiet_candidate_check,
  DROP CONSTRAINT IF EXISTS env_dispatch_run_activity_nonnegative_check,
  DROP CONSTRAINT IF EXISTS env_dispatch_run_total_timeout_check,
  DROP CONSTRAINT IF EXISTS env_dispatch_run_quiet_window_check,
  DROP CONSTRAINT IF EXISTS env_dispatch_run_status_check,
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS frozen_at,
  DROP COLUMN IF EXISTS snapshot_hash,
  DROP COLUMN IF EXISTS frozen_snapshot_id,
  DROP COLUMN IF EXISTS capture_gap_count,
  DROP COLUMN IF EXISTS unfinished_capture_batch_count,
  DROP COLUMN IF EXISTS inflight_tool_count,
  DROP COLUMN IF EXISTS queued_message_count,
  DROP COLUMN IF EXISTS pending_delivery_count,
  DROP COLUMN IF EXISTS active_turn_count,
  DROP COLUMN IF EXISTS quiet_candidate_since,
  DROP COLUMN IF EXISTS timeout_deadline_at,
  DROP COLUMN IF EXISTS initial_message_submitted_at,
  DROP COLUMN IF EXISTS total_timeout_seconds,
  DROP COLUMN IF EXISTS quiet_window_ms,
  DROP COLUMN IF EXISTS status;
