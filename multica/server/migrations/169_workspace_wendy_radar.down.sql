DROP INDEX IF EXISTS idx_agent_radar_run_active_workspace_supervisor;
DROP INDEX IF EXISTS idx_agent_radar_action_workspace_supervisor_dedupe;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_event_delivery ON agent_event_delivery;
DROP FUNCTION IF EXISTS journal_workspace_radar_event_delivery();
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_inbox_event ON agent_inbox_event;
DROP FUNCTION IF EXISTS journal_workspace_radar_inbox_event();
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_reminder ON agent_reminder;
DROP FUNCTION IF EXISTS journal_workspace_radar_reminder();
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_channel_message ON channel_message;
DROP FUNCTION IF EXISTS journal_workspace_radar_channel_message();
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_channel ON channel;
DROP FUNCTION IF EXISTS journal_workspace_radar_channel();
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_task_progress ON agent_task_progress_snapshot;
DROP FUNCTION IF EXISTS journal_workspace_radar_task_progress();
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_task ON agent_task_queue;
DROP FUNCTION IF EXISTS journal_workspace_radar_task();
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_runtime ON agent_runtime;
DROP FUNCTION IF EXISTS journal_workspace_radar_runtime();
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_agent ON agent;
DROP FUNCTION IF EXISTS journal_workspace_radar_agent();
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_comment ON comment;
DROP FUNCTION IF EXISTS journal_workspace_radar_comment();
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_issue ON issue;
DROP FUNCTION IF EXISTS journal_workspace_radar_issue();
DROP FUNCTION IF EXISTS refresh_workspace_radar_time_signals(UUID, TIMESTAMPTZ);
DROP FUNCTION IF EXISTS record_workspace_radar_change(UUID, TEXT, UUID, TIMESTAMPTZ, TEXT, UUID, JSONB);
DROP TABLE IF EXISTS workspace_radar_directive_artifact;
DROP TABLE IF EXISTS workspace_radar_time_signal;
DROP TABLE IF EXISTS workspace_radar_run_scan;
DROP TABLE IF EXISTS workspace_radar_change;
DROP TRIGGER IF EXISTS trg_guard_workspace_radar_action_insert ON agent_radar_action;
DROP FUNCTION IF EXISTS guard_workspace_radar_action_insert();
DROP TRIGGER IF EXISTS trg_guard_workspace_radar_run_success_transition ON agent_radar_run;
DROP FUNCTION IF EXISTS guard_workspace_radar_run_success_transition();
DROP TRIGGER IF EXISTS trg_guard_workspace_radar_task_dispatch ON agent_task_queue;
DROP FUNCTION IF EXISTS guard_workspace_radar_task_dispatch();
DROP FUNCTION IF EXISTS workspace_radar_task_is_authorized(UUID);
DROP TABLE IF EXISTS agent_task_progress_snapshot;
DROP TABLE IF EXISTS workspace_radar_run_state_ack;
DROP TABLE IF EXISTS workspace_radar_state;
DROP INDEX IF EXISTS idx_agent_radar_run_task_id;
ALTER TABLE agent DROP CONSTRAINT IF EXISTS uq_agent_workspace_id;
ALTER TABLE agent_radar_run DROP CONSTRAINT IF EXISTS agent_radar_run_active_scheduled_workspace_check;

-- Restore the pre-169 Radar state machine and active-run guard. A completion
-- being applied at rollback time is returned to running so the old terminal
-- reconciliation path can finish it.
DROP INDEX IF EXISTS idx_agent_radar_run_active_agent;
UPDATE agent_radar_run
SET status = 'running', updated_at = now()
WHERE status = 'executing';
ALTER TABLE agent_radar_run
  DROP CONSTRAINT IF EXISTS agent_radar_run_status_check;
ALTER TABLE agent_radar_run
  ADD CONSTRAINT agent_radar_run_status_check
  CHECK (status IN ('planned', 'queued', 'running', 'succeeded', 'no_action', 'failed', 'cancelled'));
CREATE UNIQUE INDEX idx_agent_radar_run_active_agent
  ON agent_radar_run (workspace_id, agent_id)
  WHERE status IN ('planned', 'queued', 'running');

-- Legacy Radar tasks cancelled by the up migration are intentionally not
-- restored: replaying scheduled model work during rollback would cause an
-- unexpected usage burst.
