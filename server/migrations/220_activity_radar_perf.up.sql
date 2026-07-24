-- LRM-534: indexes and @ unread counter for high-volume sidebar/activity paths.

CREATE INDEX IF NOT EXISTS idx_activity_log_actor_created_at
  ON activity_log(actor_id, created_at DESC)
  WHERE actor_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_comment_author_created_at
  ON comment(author_id, created_at DESC);

ALTER TABLE conversation_member
  ADD COLUMN IF NOT EXISTS mention_unread_count BIGINT NOT NULL DEFAULT 0;

-- Backfill once from the legacy JSONB scan semantics. Runtime writes maintain
-- this counter; mark-read recomputes the current member's value for rewinds or
-- partial reads.
WITH counts AS (
  SELECT conv.id AS conversation_id,
         cm.member_id,
         count(msg.id)::bigint AS mention_unread_count
  FROM conversation conv
  JOIN conversation_member cm
    ON cm.conversation_id = conv.id
   AND cm.member_type = 'user'
  JOIN channel_message msg
    ON msg.channel_id = conv.channel_id
   AND msg.seq > cm.last_read_seq
   AND msg.deleted_at IS NULL
   AND NOT (msg.author_type = 'user' AND msg.author_id = cm.member_id)
   AND EXISTS (
     SELECT 1
     FROM jsonb_array_elements(msg.parts) part
     WHERE part->>'type' = 'reference'
       AND part->>'ref_type' = 'mention'
       AND part->>'ref_subtype' = 'member'
       AND part->>'ref_id' = cm.member_id::text
   )
  WHERE conv.channel_id IS NOT NULL
  GROUP BY conv.id, cm.member_id
)
UPDATE conversation_member cm
SET mention_unread_count = counts.mention_unread_count,
    updated_at = now()
FROM counts
WHERE cm.conversation_id = counts.conversation_id
  AND cm.member_type = 'user'
  AND cm.member_id = counts.member_id;

CREATE INDEX IF NOT EXISTS idx_conversation_member_user_mentions
  ON conversation_member(workspace_id, member_type, member_id, mention_unread_count)
  WHERE member_type = 'user' AND mention_unread_count > 0;
