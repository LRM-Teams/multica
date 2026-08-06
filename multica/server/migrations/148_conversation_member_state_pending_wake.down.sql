DROP INDEX IF EXISTS idx_channel_ambient_pending_wake_task;
DROP INDEX IF EXISTS idx_channel_ambient_pending_wake_active;
DROP TABLE IF EXISTS channel_ambient_pending_wake;

DROP INDEX IF EXISTS idx_conversation_member_user_state;

ALTER TABLE conversation_member
  DROP COLUMN IF EXISTS closed_at,
  DROP COLUMN IF EXISTS muted_at;
