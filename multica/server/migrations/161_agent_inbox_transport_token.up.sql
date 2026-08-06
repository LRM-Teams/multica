CREATE TABLE IF NOT EXISTS agent_inbox_token (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  token_hash TEXT NOT NULL,
  inbox_event_id UUID NOT NULL REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
  delivery_id UUID NOT NULL REFERENCES agent_event_delivery(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_inbox_token_hash
  ON agent_inbox_token(token_hash);

CREATE INDEX IF NOT EXISTS idx_agent_inbox_token_event
  ON agent_inbox_token(inbox_event_id);

ALTER TABLE agent_task_transport_audit
  ADD COLUMN IF NOT EXISTS inbox_event_id UUID REFERENCES agent_inbox_event(id) ON DELETE CASCADE;

ALTER TABLE agent_task_transport_audit
  ALTER COLUMN task_id DROP NOT NULL;

-- See 160_agent_inbox_delivery: a locally interrupted migration can have
-- applied this constraint before its schema_migrations record was written.
DO $$
BEGIN
  ALTER TABLE agent_task_transport_audit
    ADD CONSTRAINT agent_task_transport_audit_source_check
    CHECK ((task_id IS NOT NULL) <> (inbox_event_id IS NOT NULL));
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS idx_agent_task_transport_audit_inbox_event
  ON agent_task_transport_audit(inbox_event_id, created_at DESC)
  WHERE inbox_event_id IS NOT NULL;
