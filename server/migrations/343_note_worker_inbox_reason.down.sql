UPDATE agent_inbox_event
SET reason = 'issue'
WHERE reason = 'note_worker';

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention','dm','ambient','thread_reply','channel_message',
    'chat_session','voice_call','issue_thread_backflow','collaboration_turn',
    'collaboration_manager_fallback','channel_onboarding','issue','quick_create',
    'autopilot','agent_radar','training','environment_dispatch','memory_curation',
    'reminder','channel_role_changed','goal_graph_delta'
  ));
