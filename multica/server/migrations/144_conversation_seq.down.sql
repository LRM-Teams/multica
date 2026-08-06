DROP INDEX IF EXISTS idx_thread_participant_member_unread;
DROP TABLE IF EXISTS thread_participant;

ALTER TABLE channel_thread_state
  DROP COLUMN IF EXISTS last_read_seq;

ALTER TABLE channel_read
  DROP COLUMN IF EXISTS last_read_seq;

DROP TRIGGER IF EXISTS trg_channel_member_conversation_remove ON channel_member;
DROP TRIGGER IF EXISTS trg_channel_member_conversation_activate ON channel_member;
DROP FUNCTION IF EXISTS remove_conversation_member();
DROP FUNCTION IF EXISTS activate_conversation_member();

DROP TABLE IF EXISTS conversation_member;

DROP TRIGGER IF EXISTS trg_channel_message_conversation_seq ON channel_message;
DROP FUNCTION IF EXISTS assign_channel_message_conversation_seq();

DROP TRIGGER IF EXISTS trg_channel_conversation ON channel;
DROP FUNCTION IF EXISTS ensure_channel_conversation();

DROP INDEX IF EXISTS idx_channel_message_thread_root_seq;
DROP INDEX IF EXISTS idx_channel_message_main_timeline_seq;
DROP INDEX IF EXISTS idx_channel_message_conversation_seq;

ALTER TABLE channel_message
  DROP CONSTRAINT IF EXISTS channel_message_conversation_fk;

ALTER TABLE channel_message
  DROP COLUMN IF EXISTS seq,
  DROP COLUMN IF EXISTS conversation_id;

DROP TABLE IF EXISTS conversation;
