CREATE TABLE IF NOT EXISTS conversation (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('group', 'dm')),
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  last_seq BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (channel_id)
);

INSERT INTO conversation (id, workspace_id, kind, channel_id, created_at, updated_at)
SELECT ch.id, ch.workspace_id, ch.kind, ch.id, ch.created_at, ch.updated_at
FROM channel ch
ON CONFLICT (channel_id) DO NOTHING;

CREATE OR REPLACE FUNCTION ensure_channel_conversation()
RETURNS TRIGGER AS $$
BEGIN
  INSERT INTO conversation (id, workspace_id, kind, channel_id, created_at, updated_at)
  VALUES (NEW.id, NEW.workspace_id, NEW.kind, NEW.id, NEW.created_at, NEW.updated_at)
  ON CONFLICT (channel_id) DO UPDATE
  SET workspace_id = EXCLUDED.workspace_id,
      kind = EXCLUDED.kind,
      updated_at = EXCLUDED.updated_at;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_channel_conversation ON channel;
CREATE TRIGGER trg_channel_conversation
AFTER INSERT ON channel
FOR EACH ROW
EXECUTE FUNCTION ensure_channel_conversation();

ALTER TABLE channel_message
  ADD COLUMN IF NOT EXISTS conversation_id UUID,
  ADD COLUMN IF NOT EXISTS seq BIGINT;

WITH numbered AS (
  SELECT
    id,
    channel_id,
    row_number() OVER (PARTITION BY channel_id ORDER BY created_at ASC, id ASC)::bigint AS seq
  FROM channel_message
)
UPDATE channel_message m
SET conversation_id = n.channel_id,
    seq = n.seq
FROM numbered n
WHERE m.id = n.id
  AND (m.conversation_id IS NULL OR m.seq IS NULL);

UPDATE conversation c
SET last_seq = COALESCE(seq_by_channel.max_seq, 0),
    updated_at = now()
FROM (
  SELECT conversation_id, max(seq) AS max_seq
  FROM channel_message
  GROUP BY conversation_id
) seq_by_channel
WHERE c.id = seq_by_channel.conversation_id;

ALTER TABLE channel_message
  ALTER COLUMN conversation_id SET NOT NULL,
  ALTER COLUMN seq SET NOT NULL;

ALTER TABLE channel_message
  ADD CONSTRAINT channel_message_conversation_fk
  FOREIGN KEY (conversation_id) REFERENCES conversation(id) ON DELETE CASCADE;

CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_message_conversation_seq
  ON channel_message(conversation_id, seq);

CREATE INDEX IF NOT EXISTS idx_channel_message_main_timeline_seq
  ON channel_message(channel_id, workspace_id, seq DESC)
  WHERE thread_root_message_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_channel_message_thread_root_seq
  ON channel_message(channel_id, thread_root_message_id, seq DESC)
  WHERE thread_root_message_id IS NOT NULL;

CREATE OR REPLACE FUNCTION assign_channel_message_conversation_seq()
RETURNS TRIGGER AS $$
DECLARE
  conv_id UUID;
  next_seq BIGINT;
BEGIN
  INSERT INTO conversation (id, workspace_id, kind, channel_id)
  SELECT NEW.channel_id, NEW.workspace_id, ch.kind, NEW.channel_id
  FROM channel ch
  WHERE ch.id = NEW.channel_id
  ON CONFLICT (channel_id) DO NOTHING;

  UPDATE conversation
  SET last_seq = last_seq + 1,
      updated_at = now()
  WHERE channel_id = NEW.channel_id
  RETURNING id, last_seq INTO conv_id, next_seq;

  NEW.conversation_id = conv_id;
  NEW.seq = next_seq;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_channel_message_conversation_seq ON channel_message;
CREATE TRIGGER trg_channel_message_conversation_seq
BEFORE INSERT ON channel_message
FOR EACH ROW
WHEN (NEW.conversation_id IS NULL OR NEW.seq IS NULL)
EXECUTE FUNCTION assign_channel_message_conversation_seq();

CREATE TABLE IF NOT EXISTS conversation_member (
  conversation_id UUID NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  member_type TEXT NOT NULL CHECK (member_type IN ('user', 'agent')),
  member_id UUID NOT NULL,
  role TEXT NOT NULL DEFAULT 'member',
  wake_state TEXT NOT NULL DEFAULT 'active' CHECK (wake_state IN ('active', 'no_wake', 'removed')),
  last_read_seq BIGINT NOT NULL DEFAULT 0,
  last_delivered_seq BIGINT NOT NULL DEFAULT 0,
  followed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (conversation_id, member_type, member_id)
);

INSERT INTO conversation_member (
  conversation_id,
  workspace_id,
  member_type,
  member_id,
  last_read_seq,
  followed_at,
  created_at,
  updated_at
)
SELECT
  c.id,
  cm.workspace_id,
  cm.member_type,
  cm.member_id,
  COALESCE(cr_seq.last_read_seq, 0),
  CASE WHEN cm.member_type = 'user' THEN cm.created_at ELSE NULL END,
  cm.created_at,
  cm.created_at
FROM channel_member cm
JOIN conversation c ON c.channel_id = cm.channel_id
LEFT JOIN channel_read cr ON cr.channel_id = cm.channel_id
  AND cr.user_id = cm.member_id
  AND cm.member_type = 'user'
LEFT JOIN LATERAL (
  SELECT max(m.seq) AS last_read_seq
  FROM channel_message m
  WHERE m.channel_id = cm.channel_id
    AND m.created_at <= cr.last_read_at
) cr_seq ON true
ON CONFLICT (conversation_id, member_type, member_id) DO NOTHING;

CREATE OR REPLACE FUNCTION activate_conversation_member()
RETURNS TRIGGER AS $$
BEGIN
  INSERT INTO conversation_member (
    conversation_id,
    workspace_id,
    member_type,
    member_id,
    wake_state,
    followed_at,
    created_at,
    updated_at
  )
  SELECT
    c.id,
    NEW.workspace_id,
    NEW.member_type,
    NEW.member_id,
    'active',
    CASE WHEN NEW.member_type = 'user' THEN NEW.created_at ELSE NULL END,
    NEW.created_at,
    now()
  FROM conversation c
  WHERE c.channel_id = NEW.channel_id
  ON CONFLICT (conversation_id, member_type, member_id) DO UPDATE
  SET wake_state = 'active',
      followed_at = COALESCE(conversation_member.followed_at, EXCLUDED.followed_at),
      updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION remove_conversation_member()
RETURNS TRIGGER AS $$
BEGIN
  UPDATE conversation_member cm
  SET wake_state = 'removed',
      followed_at = NULL,
      updated_at = now()
  FROM conversation c
  WHERE c.channel_id = OLD.channel_id
    AND cm.conversation_id = c.id
    AND cm.member_type = OLD.member_type
    AND cm.member_id = OLD.member_id;
  RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_channel_member_conversation_activate ON channel_member;
CREATE TRIGGER trg_channel_member_conversation_activate
AFTER INSERT ON channel_member
FOR EACH ROW
EXECUTE FUNCTION activate_conversation_member();

DROP TRIGGER IF EXISTS trg_channel_member_conversation_remove ON channel_member;
CREATE TRIGGER trg_channel_member_conversation_remove
AFTER DELETE ON channel_member
FOR EACH ROW
EXECUTE FUNCTION remove_conversation_member();

ALTER TABLE channel_read
  ADD COLUMN IF NOT EXISTS last_read_seq BIGINT NOT NULL DEFAULT 0;

UPDATE channel_read cr
SET last_read_seq = COALESCE((
  SELECT max(m.seq)
  FROM channel_message m
  WHERE m.channel_id = cr.channel_id
    AND m.created_at <= cr.last_read_at
), 0);

ALTER TABLE channel_thread_state
  ADD COLUMN IF NOT EXISTS last_read_seq BIGINT NOT NULL DEFAULT 0;

UPDATE channel_thread_state cts
SET last_read_seq = COALESCE((
  SELECT max(m.seq)
  FROM channel_message m
  WHERE m.channel_id = cts.channel_id
    AND (m.id = cts.root_message_id OR m.thread_root_message_id = cts.root_message_id)
    AND cts.last_read_at IS NOT NULL
    AND m.created_at <= cts.last_read_at
), 0);

CREATE TABLE IF NOT EXISTS thread_participant (
  conversation_id UUID NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
  root_message_id UUID NOT NULL REFERENCES channel_message(id) ON DELETE CASCADE,
  member_type TEXT NOT NULL CHECK (member_type IN ('user', 'agent')),
  member_id UUID NOT NULL,
  role TEXT NOT NULL DEFAULT 'participant',
  wake_state TEXT NOT NULL DEFAULT 'active' CHECK (wake_state IN ('active', 'no_wake', 'removed')),
  last_read_seq BIGINT NOT NULL DEFAULT 0,
  followed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (root_message_id, member_type, member_id)
);

INSERT INTO thread_participant (
  conversation_id,
  root_message_id,
  member_type,
  member_id,
  last_read_seq,
  followed_at,
  created_at,
  updated_at
)
SELECT
  root.conversation_id,
  cts.root_message_id,
  'user',
  cts.user_id,
  cts.last_read_seq,
  cts.followed_at,
  cts.created_at,
  cts.updated_at
FROM channel_thread_state cts
JOIN channel_message root ON root.id = cts.root_message_id
ON CONFLICT (root_message_id, member_type, member_id) DO NOTHING;

CREATE INDEX IF NOT EXISTS idx_thread_participant_member_unread
  ON thread_participant(member_type, member_id, followed_at, last_read_seq);
