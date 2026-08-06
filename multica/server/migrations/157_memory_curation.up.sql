CREATE TABLE IF NOT EXISTS memory_curation_run (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID REFERENCES agent(id) ON DELETE CASCADE,
  stage TEXT NOT NULL CHECK (stage IN ('l1_daily', 'l2_review', 'l3_promote', 'l4_curator', 'all')),
  trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('scheduled', 'manual', 'backfill')),
  status TEXT NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
  date_from DATE,
  date_to DATE,
  dry_run BOOLEAN NOT NULL DEFAULT false,
  force BOOLEAN NOT NULL DEFAULT false,
  stats JSONB NOT NULL DEFAULT '{}'::jsonb,
  error TEXT NOT NULL DEFAULT '',
  requested_by UUID REFERENCES member(id) ON DELETE SET NULL,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_memory_curation_run_workspace_created
  ON memory_curation_run(workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_curation_run_agent_created
  ON memory_curation_run(agent_id, created_at DESC) WHERE agent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_memory_curation_run_status
  ON memory_curation_run(status, created_at DESC);

CREATE TABLE IF NOT EXISTS memory_curation_watermark (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  stage TEXT NOT NULL CHECK (stage IN ('l1_daily', 'l2_review', 'l3_promote', 'l4_curator')),
  last_processed_date DATE,
  last_input_hash TEXT NOT NULL DEFAULT '',
  last_run_id UUID REFERENCES memory_curation_run(id) ON DELETE SET NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, agent_id, stage)
);
