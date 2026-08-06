CREATE TABLE IF NOT EXISTS memory_curation_agent_run (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  parent_run_id UUID NOT NULL REFERENCES memory_curation_run(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
  stage TEXT NOT NULL CHECK (stage IN ('agent_self_review')),
  status TEXT NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'waiting_runtime', 'running', 'succeeded', 'failed', 'cancelled', 'skipped')),
  attempt INT NOT NULL DEFAULT 0,
  claimed_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  error TEXT NOT NULL DEFAULT '',
  stats JSONB NOT NULL DEFAULT '{}'::jsonb,
  output JSONB NOT NULL DEFAULT '{}'::jsonb,
  claim_token UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (parent_run_id, agent_id, stage)
);

CREATE INDEX IF NOT EXISTS idx_memory_curation_agent_run_parent_created
  ON memory_curation_agent_run(parent_run_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_curation_agent_run_workspace_status
  ON memory_curation_agent_run(workspace_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_curation_agent_run_agent_created
  ON memory_curation_agent_run(agent_id, created_at DESC);

ALTER TABLE agent_memory_write_event
  DROP CONSTRAINT IF EXISTS agent_memory_write_event_scope_type_check,
  ADD CONSTRAINT agent_memory_write_event_scope_type_check
    CHECK (scope_type IN ('agent_global', 'agent_state', 'agent_daily', 'agent_notes', 'user', 'channel', 'project'));

ALTER TABLE agent_memory_curation_candidate
  DROP CONSTRAINT IF EXISTS agent_memory_curation_candidate_candidate_type_check,
  ADD CONSTRAINT agent_memory_curation_candidate_candidate_type_check
    CHECK (candidate_type IN ('memory', 'user_preference', 'relationship', 'project_fact', 'project_state', 'decision', 'state', 'skill', 'team_memory', 'team_skill', 'conflict', 'follow_up')),
  DROP CONSTRAINT IF EXISTS agent_memory_curation_candidate_scope_check,
  ADD CONSTRAINT agent_memory_curation_candidate_scope_check
    CHECK (scope IN ('agent', 'user', 'project', 'channel', 'workspace', 'team'));
