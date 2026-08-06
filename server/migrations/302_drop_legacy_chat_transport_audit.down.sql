-- Restore the immediately preceding schema for a database rollback. Current
-- application code deliberately does not read or write these retired tables.
-- agent_task_queue was retired in migration 223, so task_id is a nullable
-- compatibility key here rather than a foreign key to a table that no longer
-- exists in the immediately preceding schema.
CREATE TABLE IF NOT EXISTS agent_task_transport_audit (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  task_id UUID,
  inbox_event_id UUID REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  action TEXT NOT NULL CHECK (action IN ('message_send', 'message_react', 'message_read', 'message_search', 'thread_unfollow')),
  target TEXT NOT NULL DEFAULT '',
  channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  channel_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  client_message_id TEXT,
  context_pack JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT agent_task_transport_audit_source_check
    CHECK ((task_id IS NOT NULL) <> (inbox_event_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_agent_task_transport_audit_task
  ON agent_task_transport_audit(task_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_task_transport_audit_workspace
  ON agent_task_transport_audit(workspace_id, agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_task_transport_audit_inbox_event
  ON agent_task_transport_audit(inbox_event_id, created_at DESC)
  WHERE inbox_event_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_task_transport_audit_agent
  ON agent_task_transport_audit(agent_id);

CREATE TABLE IF NOT EXISTS agent_transport_draft (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  target TEXT NOT NULL,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  thread_root_message_id UUID REFERENCES channel_message(id) ON DELETE CASCADE,
  content TEXT NOT NULL DEFAULT '',
  parts JSONB NOT NULL DEFAULT '[]'::jsonb,
  client_message_id TEXT NOT NULL,
  seen_up_to_seq BIGINT NOT NULL DEFAULT 0 CHECK (seen_up_to_seq >= 0),
  held_from_seq BIGINT NOT NULL DEFAULT 0 CHECK (held_from_seq >= 0),
  held_to_seq BIGINT NOT NULL DEFAULT 0 CHECK (held_to_seq >= held_from_seq),
  shown_from_seq BIGINT,
  shown_to_seq BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  task_id UUID,
  inbox_event_id UUID REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
  decision_fact_id TEXT NOT NULL,
  CONSTRAINT agent_transport_draft_source_check
    CHECK ((task_id IS NOT NULL) <> (inbox_event_id IS NOT NULL)),
  CONSTRAINT agent_transport_draft_decision_fact_nonempty_check
    CHECK (btrim(decision_fact_id) <> '')
);

CREATE INDEX IF NOT EXISTS idx_agent_transport_draft_agent_updated
  ON agent_transport_draft(workspace_id, agent_id, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_transport_draft_source_target
  ON agent_transport_draft(workspace_id, agent_id, task_id, target)
  WHERE task_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_transport_draft_inbox_target
  ON agent_transport_draft(workspace_id, agent_id, inbox_event_id, target)
  WHERE inbox_event_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_transport_draft_agent
  ON agent_transport_draft(agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_transport_draft_inbox_event
  ON agent_transport_draft(inbox_event_id)
  WHERE inbox_event_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_transport_draft_task
  ON agent_transport_draft(task_id)
  WHERE task_id IS NOT NULL;

-- agent_task_queue was retired in migration 223. Preserve the historical
-- column as a compatibility value only, without a foreign key to that table.
CREATE TABLE IF NOT EXISTS channel_ambient_pending_wake (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id UUID NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  chat_session_id UUID REFERENCES chat_session(id) ON DELETE SET NULL,
  task_id UUID,
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
CREATE INDEX IF NOT EXISTS idx_channel_ambient_pending_wake_agent
  ON channel_ambient_pending_wake(agent_id);
CREATE INDEX IF NOT EXISTS idx_channel_ambient_pending_wake_chat_session
  ON channel_ambient_pending_wake(chat_session_id)
  WHERE chat_session_id IS NOT NULL;
