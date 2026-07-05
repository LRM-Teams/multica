CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_message_agent_client_idempotency
  ON channel_message (workspace_id, channel_id, author_id, client_message_id)
  WHERE author_type = 'agent' AND client_message_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS agent_task_transport_audit (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  task_id UUID NOT NULL REFERENCES agent_task_queue(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  action TEXT NOT NULL CHECK (action IN ('message_send', 'message_react', 'message_read', 'message_search')),
  target TEXT NOT NULL DEFAULT '',
  channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  channel_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  client_message_id TEXT,
  context_pack JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_agent_task_transport_audit_task
  ON agent_task_transport_audit(task_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_task_transport_audit_workspace
  ON agent_task_transport_audit(workspace_id, agent_id, created_at DESC);
