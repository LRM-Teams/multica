CREATE TABLE IF NOT EXISTS agent_session (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
  conversation_id UUID REFERENCES conversation(id) ON DELETE CASCADE,
  channel_id UUID REFERENCES channel(id) ON DELETE CASCADE,
  chat_session_id UUID REFERENCES chat_session(id) ON DELETE CASCADE,
  scope TEXT NOT NULL CHECK (scope IN ('channel', 'dm', 'direct_chat')),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'closed')),
  last_drained_seq BIGINT NOT NULL DEFAULT 0,
  last_acked_event_id UUID,
  lease_token UUID,
  lease_expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (last_drained_seq >= 0),
  UNIQUE (workspace_id, agent_id, conversation_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_session_runtime_active
  ON agent_session(runtime_id, status, updated_at DESC)
  WHERE runtime_id IS NOT NULL AND status = 'active';

CREATE INDEX IF NOT EXISTS idx_agent_session_chat_session
  ON agent_session(chat_session_id)
  WHERE chat_session_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS agent_inbox_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_session_id UUID REFERENCES agent_session(id) ON DELETE CASCADE,
  conversation_id UUID REFERENCES conversation(id) ON DELETE CASCADE,
  channel_id UUID REFERENCES channel(id) ON DELETE CASCADE,
  chat_session_id UUID REFERENCES chat_session(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  source_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  reason TEXT NOT NULL CHECK (reason IN ('mention', 'dm', 'ambient')),
  requires_wake BOOLEAN NOT NULL DEFAULT FALSE,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'draining', 'acked', 'failed', 'suppressed')),
  priority INT NOT NULL DEFAULT 0,
  seq_from BIGINT NOT NULL DEFAULT 0,
  seq_to BIGINT NOT NULL DEFAULT 0,
  attempt INT NOT NULL DEFAULT 0,
  last_error TEXT,
  claimed_at TIMESTAMPTZ,
  acked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (seq_from >= 0),
  CHECK (seq_to >= seq_from)
);

-- The migration runner records a version only after the full file succeeds.
-- Keep this constraint idempotent so a local database that was interrupted
-- after creating it can resume the rest of this migration on the next run.
DO $$
BEGIN
  ALTER TABLE agent_session
    ADD CONSTRAINT agent_session_last_acked_event_fk
    FOREIGN KEY (last_acked_event_id) REFERENCES agent_inbox_event(id) ON DELETE SET NULL;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS idx_agent_inbox_event_pending
  ON agent_inbox_event(workspace_id, agent_id, status, priority DESC, created_at ASC, id ASC)
  WHERE status IN ('pending', 'failed');

CREATE INDEX IF NOT EXISTS idx_agent_inbox_event_conversation
  ON agent_inbox_event(conversation_id, agent_id, status, seq_to DESC)
  WHERE conversation_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agent_inbox_event_source_message
  ON agent_inbox_event(source_message_id)
  WHERE source_message_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_inbox_event_ambient_pending_unique
  ON agent_inbox_event(conversation_id, agent_id)
  WHERE reason = 'ambient' AND status IN ('pending', 'failed') AND conversation_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS agent_event_delivery (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_session_id UUID NOT NULL REFERENCES agent_session(id) ON DELETE CASCADE,
  inbox_event_id UUID NOT NULL REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
  runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'leased' CHECK (status IN ('leased', 'processing', 'acked', 'failed', 'expired')),
  lease_token UUID NOT NULL DEFAULT gen_random_uuid(),
  leased_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '2 minutes',
  acked_at TIMESTAMPTZ,
  last_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_agent_event_delivery_event_created
  ON agent_event_delivery(inbox_event_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_event_delivery_session_active
  ON agent_event_delivery(agent_session_id, status, lease_expires_at ASC)
  WHERE status IN ('leased', 'processing');
