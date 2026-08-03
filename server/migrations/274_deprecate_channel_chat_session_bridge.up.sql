-- LRM-1079 / LRM-1082 P3: mark the channel↔chat_session bridge as deprecated.
--
-- Safety: COMMENT-only (no DROP / no column removal). Runtime does not read
-- these comments; they document intent for operators and future DROP work.
--
-- Do NOT DROP yet: env-dispatch, onboarding, voice-call, and standalone Agent
-- Chat still read these tables. Ordinary mention/DM/reminder/ambient wakes are
-- channel-only (LRM-1081). Full DROP requires a follow-up after those leftover
-- readers migrate.
--
-- Rollback: see 274_deprecate_channel_chat_session_bridge.down.sql
-- (clears COMMENTs only; no data restore).

COMMENT ON TABLE chat_session IS
  'DEPRECATED for channel product surface (LRM-1079). Product anchors on channel_id. Retained for standalone Agent Chat, env-dispatch, and onboarding bridges until those readers migrate.';

COMMENT ON TABLE channel_agent_session IS
  'DEPRECATED bridge from channel_id → chat_session_id (LRM-1079). Ordinary channel wakes no longer create rows here. Retained for env-dispatch / legacy ChatDone lookup until DROP.';

COMMENT ON COLUMN agent_inbox_event.chat_session_id IS
  'Optional legacy bridge. Channel product wakes use channel_id + context.channel_wake; chat_session_id may be NULL.';
