ALTER TABLE memory_curator_profile
  ADD COLUMN IF NOT EXISTS self_review_enabled BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS team_curation_enabled BOOLEAN NOT NULL DEFAULT false;

-- The new agentic system is opt-in even for workspaces that enabled the old L1-L4 pipeline.
UPDATE memory_curator_profile
   SET enabled = false,
       self_review_enabled = false,
       team_curation_enabled = false;

ALTER TABLE memory_curation_run
  DROP CONSTRAINT IF EXISTS memory_curation_run_stage_check,
  ADD CONSTRAINT memory_curation_run_stage_check
    CHECK (stage IN ('agent_self_review', 'team_curation', 'all', 'l1_daily', 'l2_review', 'l3_promote', 'l4_curator'));

ALTER TABLE memory_curation_watermark
  DROP CONSTRAINT IF EXISTS memory_curation_watermark_stage_check,
  ADD CONSTRAINT memory_curation_watermark_stage_check
    CHECK (stage IN ('agent_self_review', 'team_curation', 'l1_daily', 'l2_review', 'l3_promote', 'l4_curator'));

CREATE TABLE IF NOT EXISTS agent_memory_curation_candidate (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  source_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  run_id UUID REFERENCES memory_curation_run(id) ON DELETE SET NULL,
  candidate_type TEXT NOT NULL CHECK (candidate_type IN ('memory', 'user_preference', 'state', 'skill', 'team_memory', 'team_skill', 'conflict', 'follow_up')),
  scope TEXT NOT NULL DEFAULT 'agent' CHECK (scope IN ('agent', 'user', 'workspace', 'team')),
  title TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (confidence >= 0 AND confidence <= 1),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'promoted', 'rejected', 'merged', 'superseded')),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_agent_memory_curation_candidate_workspace_status
  ON agent_memory_curation_candidate(workspace_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_memory_curation_candidate_source_agent
  ON agent_memory_curation_candidate(source_agent_id, created_at DESC) WHERE source_agent_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS team_knowledge_item (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('memory', 'pattern', 'skill', 'policy', 'troubleshooting')),
  title TEXT NOT NULL,
  content TEXT NOT NULL,
  source_candidate_ids UUID[] NOT NULL DEFAULT '{}',
  created_by_curator_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_team_knowledge_item_workspace_kind
  ON team_knowledge_item(workspace_id, kind, created_at DESC);
