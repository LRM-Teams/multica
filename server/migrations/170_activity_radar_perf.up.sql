CREATE INDEX IF NOT EXISTS idx_activity_log_actor_created
  ON activity_log(actor_id, created_at DESC)
  WHERE actor_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_comment_author_created
  ON comment(author_id, created_at DESC);

CREATE TABLE IF NOT EXISTS channel_message_user_mention (
    channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    message_id UUID NOT NULL REFERENCES channel_message(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_channel_message_user_mention_unread
  ON channel_message_user_mention(channel_id, user_id, seq);

INSERT INTO channel_message_user_mention (channel_id, message_id, seq, user_id, created_at)
SELECT m.channel_id, m.id, m.seq, u.id, m.created_at
FROM channel_message m
JOIN channel_member cm
  ON cm.channel_id = m.channel_id
 AND cm.workspace_id = m.workspace_id
 AND cm.member_type = 'user'
JOIN "user" u ON u.id = cm.member_id
WHERE m.deleted_at IS NULL
  AND NOT (m.author_type = 'user' AND m.author_id = u.id)
  AND (
    m.content ILIKE '%@' || u.name || '%'
    OR m.parts::text ILIKE '%mention://' || u.id::text || '%'
  )
ON CONFLICT DO NOTHING;
