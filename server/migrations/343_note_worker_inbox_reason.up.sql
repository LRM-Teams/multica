-- Allow Note Worker product inbox reason (Messages-timeline 「按这篇做」).
-- Residual channel dual-write reasons (mention/dm/…) stay suppressed on drain;
-- note_worker is a retained product surface like goal_graph_delta.

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
