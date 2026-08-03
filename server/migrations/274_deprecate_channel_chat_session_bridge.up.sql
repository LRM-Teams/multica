-- LRM-1079 / LRM-1082 P3: mark the channel↔chat_session bridge as deprecated.
-- Do NOT DROP yet: env-dispatch, onboarding, and standalone Agent Chat still
-- read these tables. Ordinary mention/DM/reminder/ambient wakes are channel-only
-- (LRM-1081). Full DROP requires a follow-up after those leftover readers migrate.

COMMENT ON TABLE chat_session IS
  'DEPRECATED for channel product surface (LRM-1079). Product anchors on channel_id. Retained for standalone Agent Chat, env-dispatch, and onboarding bridges until those readers migrate.';

COMMENT ON TABLE channel_agent_session IS
  'DEPRECATED bridge from channel_id → chat_session_id (LRM-1079). Ordinary channel wakes no longer create rows here. Retained for env-dispatch / legacy ChatDone lookup until DROP.';

COMMENT ON COLUMN agent_inbox_event.chat_session_id IS
  'Optional legacy bridge. Channel product wakes use channel_id + context.channel_wake; chat_session_id may be NULL.';
