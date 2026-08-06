DROP INDEX IF EXISTS idx_channel_thread_state_user_unread;

DROP TABLE IF EXISTS channel_thread_state;

DROP INDEX IF EXISTS idx_channel_message_thread_root_created;

ALTER TABLE chat_message
  DROP COLUMN IF EXISTS channel_thread_root_message_id;

ALTER TABLE channel_message
  DROP COLUMN IF EXISTS thread_root_message_id;
