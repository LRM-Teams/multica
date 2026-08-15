BEGIN;

-- Restart is the only product operation persisted here. Rename the table and
-- every live schema object so the retired composite lifecycle contract cannot
-- be rediscovered through the current database schema.
ALTER TABLE agent_lifecycle_operation RENAME TO agent_restart_operation;
ALTER TABLE agent_restart_operation
  RENAME CONSTRAINT agent_lifecycle_operation_pkey TO agent_restart_operation_pkey;
ALTER TABLE agent_restart_operation
  RENAME CONSTRAINT agent_lifecycle_operation_workspace_id_fkey TO agent_restart_operation_workspace_id_fkey;
ALTER TABLE agent_restart_operation
  RENAME CONSTRAINT agent_lifecycle_operation_agent_id_fkey TO agent_restart_operation_agent_id_fkey;
ALTER TABLE agent_restart_operation
  RENAME CONSTRAINT agent_lifecycle_operation_runtime_id_fkey TO agent_restart_operation_runtime_id_fkey;
ALTER TABLE agent_restart_operation
  RENAME CONSTRAINT agent_lifecycle_operation_actor_user_id_fkey TO agent_restart_operation_actor_user_id_fkey;
ALTER TABLE agent_restart_operation
  RENAME CONSTRAINT agent_lifecycle_operation_agent_id_idempotency_key_key TO agent_restart_operation_agent_id_idempotency_key_key;
ALTER TABLE agent_restart_operation
  RENAME CONSTRAINT agent_lifecycle_operation_action_kind_check TO agent_restart_operation_action_kind_check;
ALTER TABLE agent_restart_operation
  RENAME CONSTRAINT agent_lifecycle_operation_status_check TO agent_restart_operation_status_check;
ALTER TABLE agent_restart_operation
  RENAME CONSTRAINT agent_lifecycle_operation_execution_mode_check TO agent_restart_operation_execution_mode_check;
ALTER TABLE agent_restart_operation
  RENAME CONSTRAINT agent_lifecycle_operation_check TO agent_restart_operation_status_timestamps_check;
ALTER INDEX agent_lifecycle_operation_one_active_per_agent_idx
  RENAME TO agent_restart_operation_one_active_per_agent_idx;
ALTER INDEX agent_lifecycle_operation_latest_idx
  RENAME TO agent_restart_operation_latest_idx;

-- Persist the exact config.sessionId selected from the stopped launch before
-- the replacement launch is dispatched. Runner reconnect can then replay the
-- same start command without consulting a second session-state projection.
ALTER TABLE agent_restart_operation
  ADD COLUMN start_session_id TEXT NOT NULL DEFAULT ''
  CHECK (length(start_session_id) <= 200);

-- This table never completed its advertised canonical cutover. The real
-- provider session fact is launch-scoped agent:session; keeping both would
-- preserve two competing owners for restart semantics.
DROP TABLE agent_runtime_state;

COMMIT;
