DROP TRIGGER IF EXISTS issue_run_goal_controller_event ON agent_inbox_event;
DROP FUNCTION IF EXISTS issue_run_goal_controller_event_trigger();
DROP TRIGGER IF EXISTS issue_dependency_goal_controller_event ON issue_dependency;
DROP FUNCTION IF EXISTS issue_dependency_goal_controller_event_trigger();
DROP TRIGGER IF EXISTS issue_goal_controller_event ON issue;
DROP FUNCTION IF EXISTS issue_goal_controller_event_trigger();
DROP TRIGGER IF EXISTS channel_goal_controller_event ON channel_goal;
DROP FUNCTION IF EXISTS channel_goal_controller_event_trigger();
DROP FUNCTION IF EXISTS enqueue_goal_controller_event(UUID, UUID, TEXT, TEXT, UUID, JSONB);
DROP TABLE IF EXISTS goal_controller_event;

-- Retire controller Runs before removing their product reason. Terminal audit
-- rows are retained; all rows are remapped only after no Run can execute.
UPDATE agent_inbox_event
SET status='suppressed', terminal_outcome='cancelled', terminal_at=now(),
    requires_wake=false, failure_reason='goal controller rolled back', updated_at=now()
WHERE reason='goal_controller'
  AND terminal_outcome IS NULL
  AND status IN ('pending','draining','failed');
UPDATE agent_inbox_event
SET reason='issue', updated_at=now()
WHERE reason='goal_controller';

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;
ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention','dm','ambient','thread_reply','channel_message',
    'chat_session','voice_call','issue_thread_backflow','collaboration_turn',
    'collaboration_manager_fallback','channel_onboarding','issue','quick_create',
    'autopilot','agent_radar','training','environment_dispatch','memory_curation',
    'reminder','channel_role_changed','goal_graph_delta','note_worker'
  ));
