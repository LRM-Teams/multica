CREATE TABLE IF NOT EXISTS agent_transport_draft (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  target TEXT NOT NULL,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  thread_root_message_id UUID REFERENCES channel_message(id) ON DELETE CASCADE,
  content TEXT NOT NULL DEFAULT '',
  parts JSONB NOT NULL DEFAULT '[]'::jsonb,
  options JSONB NOT NULL DEFAULT '{}'::jsonb,
  client_message_id TEXT NOT NULL,
  seen_up_to_seq BIGINT NOT NULL DEFAULT 0 CHECK (seen_up_to_seq >= 0),
  held_from_seq BIGINT NOT NULL DEFAULT 0 CHECK (held_from_seq >= 0),
  held_to_seq BIGINT NOT NULL DEFAULT 0 CHECK (held_to_seq >= held_from_seq),
  shown_from_seq BIGINT,
  shown_to_seq BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, agent_id, target)
);

CREATE INDEX IF NOT EXISTS idx_agent_transport_draft_agent_updated
  ON agent_transport_draft(workspace_id, agent_id, updated_at DESC);
