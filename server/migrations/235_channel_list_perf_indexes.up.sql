-- Renumbered from 233_channel_list_perf_indexes (task #785).
-- Collision: #1257/#1258 already used 233_restore / 234_kill for wendy_ambient.
-- Semantics unchanged; statements stay idempotent for environments that already
-- applied the old 233_channel_list_perf_indexes ledger name.

-- LRM-632: keep GET /api/channels first-screen enrichments bounded.

ALTER TABLE conversation_member
  ADD COLUMN IF NOT EXISTS main_unread_count BIGINT NOT NULL DEFAULT 0;

WITH counts AS (
  SELECT conv.id AS conversation_id,
         cm.member_id,
         count(msg.id)::bigint AS main_unread_count
  FROM conversation conv
  JOIN conversation_member cm
    ON cm.conversation_id = conv.id
   AND cm.member_type = 'user'
  JOIN channel_message msg
    ON msg.channel_id = conv.channel_id
   AND msg.seq > cm.last_read_seq
   AND msg.author_type <> 'system'
   AND NOT (msg.author_type = 'user' AND msg.author_id = cm.member_id)
   AND msg.thread_root_message_id IS NULL
   AND msg.deleted_at IS NULL
  WHERE conv.channel_id IS NOT NULL
  GROUP BY conv.id, cm.member_id
)
UPDATE conversation_member cm
SET main_unread_count = counts.main_unread_count,
    updated_at = now()
FROM counts
WHERE cm.conversation_id = counts.conversation_id
  AND cm.member_type = 'user'
  AND cm.member_id = counts.member_id;

CREATE INDEX IF NOT EXISTS idx_channel_message_list_main_seq
  ON channel_message(channel_id, workspace_id, seq DESC)
  WHERE thread_root_message_id IS NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_channel_member_avatar_stack
  ON channel_member(channel_id, workspace_id, created_at, member_type, member_id);
