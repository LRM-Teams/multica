-- task #780: full workspace radar module deletion. Application code (Go
-- handlers, scheduler job, service-layer completion hooks, daemon prompt
-- wiring) was removed in the companion PR; this migration drops the schema
-- it left behind: the change-journal triggers on 10 shared tables, their
-- trigger/helper functions, and the radar-owned tables themselves.
-- (wendy_channel_ambient.active_radar_run_id needs no cleanup here — that
-- table was already dropped whole by migration 247.)

DROP TRIGGER IF EXISTS trg_journal_workspace_radar_agent ON agent;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_event_delivery ON agent_event_delivery;
DROP TRIGGER IF EXISTS trg_guard_workspace_radar_task_dispatch ON agent_inbox_event;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_inbox_event ON agent_inbox_event;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_task ON agent_inbox_event;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_reminder ON agent_reminder;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_runtime ON agent_runtime;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_task_progress ON agent_task_progress_snapshot;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_channel ON channel;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_channel_message ON channel_message;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_comment ON comment;
DROP TRIGGER IF EXISTS trg_journal_workspace_radar_issue ON issue;
DROP TRIGGER IF EXISTS trg_guard_workspace_radar_action_insert ON agent_radar_action;
DROP TRIGGER IF EXISTS trg_guard_workspace_radar_run_success_transition ON agent_radar_run;

DROP FUNCTION IF EXISTS guard_workspace_radar_task_dispatch();
DROP FUNCTION IF EXISTS journal_workspace_radar_agent();
DROP FUNCTION IF EXISTS journal_workspace_radar_event_delivery();
DROP FUNCTION IF EXISTS journal_workspace_radar_inbox_event();
DROP FUNCTION IF EXISTS journal_workspace_radar_task();
DROP FUNCTION IF EXISTS journal_workspace_radar_reminder();
DROP FUNCTION IF EXISTS journal_workspace_radar_runtime();
DROP FUNCTION IF EXISTS journal_workspace_radar_task_progress();
DROP FUNCTION IF EXISTS journal_workspace_radar_channel();
DROP FUNCTION IF EXISTS journal_workspace_radar_channel_message();
DROP FUNCTION IF EXISTS journal_workspace_radar_comment();
DROP FUNCTION IF EXISTS journal_workspace_radar_issue();
DROP FUNCTION IF EXISTS guard_workspace_radar_action_insert();
DROP FUNCTION IF EXISTS guard_workspace_radar_run_success_transition();
DROP FUNCTION IF EXISTS workspace_radar_task_is_authorized(UUID);
DROP FUNCTION IF EXISTS record_workspace_radar_change(UUID, TEXT, UUID, TIMESTAMPTZ, TEXT, UUID, JSONB);
DROP FUNCTION IF EXISTS refresh_workspace_radar_time_signals(UUID, TIMESTAMPTZ);

-- Children first, then the tables they reference.
DROP TABLE IF EXISTS workspace_radar_directive_artifact;
DROP TABLE IF EXISTS workspace_radar_run_scan;
DROP TABLE IF EXISTS workspace_radar_run_state_ack;
DROP TABLE IF EXISTS agent_radar_action;
DROP TABLE IF EXISTS agent_radar_run;
DROP TABLE IF EXISTS workspace_radar_change;
DROP TABLE IF EXISTS workspace_radar_time_signal;
DROP TABLE IF EXISTS workspace_radar_state;
