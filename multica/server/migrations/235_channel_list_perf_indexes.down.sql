DROP INDEX IF EXISTS idx_channel_member_avatar_stack;
DROP INDEX IF EXISTS idx_channel_message_list_main_seq;

ALTER TABLE conversation_member
  DROP COLUMN IF EXISTS main_unread_count;
