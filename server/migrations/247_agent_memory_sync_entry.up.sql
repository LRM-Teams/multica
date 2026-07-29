CREATE TABLE IF NOT EXISTS agent_memory_sync_entry (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  identity_key TEXT NOT NULL,
  scope TEXT NOT NULL DEFAULT 'user'
    CHECK (scope IN ('agent', 'user', 'project', 'channel')),
  subject_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT 'preference',
  topic TEXT NOT NULL DEFAULT '',
  rel_path TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'conflict', 'superseded')),
  conflict_of UUID REFERENCES agent_memory_sync_entry(id) ON DELETE SET NULL,
  source_runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one active row per identity key for an agent (strategy A).
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_memory_sync_entry_active_identity
  ON agent_memory_sync_entry (agent_id, identity_key)
  WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_agent_memory_sync_entry_agent_status
  ON agent_memory_sync_entry (agent_id, status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_memory_sync_entry_workspace_agent
  ON agent_memory_sync_entry (workspace_id, agent_id, updated_at DESC);

-- Self-FK: when rows cascade-delete with the agent, PostgreSQL must SET NULL
-- conflict_of on children; without this index agent hard-delete seqscans.
CREATE INDEX IF NOT EXISTS idx_agent_memory_sync_entry_conflict_of
  ON agent_memory_sync_entry (conflict_of)
  WHERE conflict_of IS NOT NULL;
