CREATE TABLE channel_message_reaction (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_message_id UUID NOT NULL REFERENCES channel_message(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent')),
  actor_id UUID NOT NULL,
  emoji TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (channel_message_id, actor_type, actor_id, emoji)
);

CREATE INDEX idx_channel_message_reaction_message_id
  ON channel_message_reaction(channel_message_id);
