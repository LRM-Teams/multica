DROP INDEX IF EXISTS idx_conversation_member_user_mentions;

ALTER TABLE conversation_member
  DROP COLUMN IF EXISTS mention_unread_count;

DROP INDEX IF EXISTS idx_comment_author_created_at;
DROP INDEX IF EXISTS idx_activity_log_actor_created_at;
