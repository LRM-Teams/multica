CREATE TABLE IF NOT EXISTS memory_curation_evidence_cursor (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  source_kind TEXT NOT NULL,
  source_id TEXT NOT NULL DEFAULT '',
  last_seen_at TIMESTAMPTZ,
  last_seen_seq BIGINT,
  last_hash TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, agent_id, source_kind, source_id)
);

CREATE TABLE IF NOT EXISTS evolution_unit_feedback_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
  unit_type TEXT NOT NULL CHECK (unit_type IN ('memory', 'skill', 'workflow', 'tool_pattern', 'preference')),
  unit_id UUID,
  local_unit_id TEXT NOT NULL DEFAULT '',
  event TEXT NOT NULL CHECK (event IN ('injected', 'used', 'ignored', 'success', 'failure', 'conflict')),
  outcome TEXT NOT NULL DEFAULT '' CHECK (outcome IN ('', 'success', 'failure', 'neutral')),
  source TEXT NOT NULL DEFAULT 'runtime' CHECK (source IN ('runtime', 'curator', 'manual', 'system')),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_evolution_feedback_workspace_unit
  ON evolution_unit_feedback_event(workspace_id, unit_type, unit_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_evolution_feedback_agent
  ON evolution_unit_feedback_event(workspace_id, agent_id, created_at DESC);
