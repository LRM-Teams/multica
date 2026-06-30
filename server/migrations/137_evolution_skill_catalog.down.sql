CREATE TABLE evolution_unit_delivery (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  unit_id UUID NOT NULL REFERENCES shared_evolution_unit(id) ON DELETE CASCADE,
  version_id UUID REFERENCES shared_evolution_unit_version(id) ON DELETE SET NULL,
  target_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  delivery_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  reason TEXT NOT NULL DEFAULT '',
  matcher_score DOUBLE PRECISION NOT NULL DEFAULT 0,
  matcher_details JSONB NOT NULL DEFAULT '{}',
  delivered_path TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  decided_at TIMESTAMPTZ,
  delivered_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (unit_id, version_id, target_agent_id)
);

DROP TABLE IF EXISTS agent_skill_suggestion;

ALTER TABLE agent_skill DROP COLUMN IF EXISTS source;

DROP INDEX IF EXISTS idx_skill_workspace_source_evolution_unit;

ALTER TABLE skill DROP COLUMN IF EXISTS source_evolution_unit_id;
