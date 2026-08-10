-- Split product inbox reasons so standalone bubble / voice / issue-thread
-- backflow do not share residual channel dual-write reason strings.
-- After #2295, channel Message delivery is MessageCoordinator-owned;
-- residual channel reasons (mention/dm/ambient/…) remain in the set only so
-- historical rows can be suppressed on drain.

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    -- residual channel dual-write (do not execute after hard-cut)
    'mention',
    'dm',
    'ambient',
    'thread_reply',
    'channel_message',
    -- retained product surfaces
    'chat_session',
    'voice_call',
    'issue_thread_backflow',
    'collaboration_turn',
    'collaboration_manager_fallback',
    'channel_onboarding',
    'issue',
    'quick_create',
    'autopilot',
    'agent_radar',
    'training',
    'environment_dispatch',
    'memory_curation',
    'reminder',
    'channel_role_changed'
  ));
