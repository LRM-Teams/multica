ALTER TABLE conversation_member
  ADD COLUMN IF NOT EXISTS muted_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ;

UPDATE conversation_member cm
SET muted_at = chm.muted_at,
    updated_at = now()
FROM conversation c
JOIN channel_member chm ON chm.channel_id = c.channel_id
WHERE cm.conversation_id = c.id
  AND cm.member_type = chm.member_type
  AND cm.member_id = chm.member_id
  AND chm.muted_at IS NOT NULL
  AND cm.muted_at IS DISTINCT FROM chm.muted_at;

WITH dm_state AS (
  SELECT
    c.id AS conversation_id,
    state.user_id,
    state.muted_at,
    state.hidden_at AS closed_at
  FROM conversation c
  JOIN channel ch ON ch.id = c.channel_id AND ch.kind = 'dm'
  JOIN channel_member self
    ON self.channel_id = ch.id
   AND self.member_type = 'user'
  JOIN channel_member peer
    ON peer.channel_id = ch.id
   AND NOT (peer.member_type = 'user' AND peer.member_id = self.member_id)
  JOIN dm_peer_state state
    ON state.workspace_id = c.workspace_id
   AND state.user_id = self.member_id
   AND state.peer_type = peer.member_type
   AND state.peer_id = peer.member_id
)
UPDATE conversation_member cm
SET muted_at = dm_state.muted_at,
    closed_at = dm_state.closed_at,
    updated_at = now()
FROM dm_state
WHERE cm.conversation_id = dm_state.conversation_id
  AND cm.member_type = 'user'
  AND cm.member_id = dm_state.user_id
  AND (
    cm.muted_at IS DISTINCT FROM dm_state.muted_at
    OR cm.closed_at IS DISTINCT FROM dm_state.closed_at
  );

CREATE INDEX IF NOT EXISTS idx_conversation_member_user_state
  ON conversation_member(workspace_id, member_type, member_id, closed_at, muted_at, last_read_seq);

CREATE TABLE IF NOT EXISTS channel_ambient_pending_wake (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id UUID NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  chat_session_id UUID REFERENCES chat_session(id) ON DELETE SET NULL,
  task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'completed', 'failed')),
  pending_from_seq BIGINT NOT NULL DEFAULT 0,
  pending_to_seq BIGINT NOT NULL DEFAULT 0,
  delivered_to_seq BIGINT NOT NULL DEFAULT 0,
  last_trigger_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  last_decision TEXT NOT NULL DEFAULT 'accepted',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  UNIQUE (conversation_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_channel_ambient_pending_wake_active
  ON channel_ambient_pending_wake(workspace_id, channel_id, agent_id, status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_channel_ambient_pending_wake_task
  ON channel_ambient_pending_wake(task_id)
  WHERE task_id IS NOT NULL;
