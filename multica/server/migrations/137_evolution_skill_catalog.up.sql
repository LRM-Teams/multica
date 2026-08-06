-- Evolution skill catalog: promote → skill table, agent suggestions, drop per-agent delivery.

ALTER TABLE skill
  ADD COLUMN source_evolution_unit_id UUID REFERENCES shared_evolution_unit(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX idx_skill_workspace_source_evolution_unit
  ON skill(workspace_id, source_evolution_unit_id)
  WHERE source_evolution_unit_id IS NOT NULL;

ALTER TABLE agent_skill
  ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'
  CHECK (source IN ('manual', 'evolution', 'template'));

CREATE TABLE agent_skill_suggestion (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  skill_id UUID NOT NULL REFERENCES skill(id) ON DELETE CASCADE,
  action TEXT NOT NULL CHECK (action IN ('add', 'remove')),
  reason TEXT NOT NULL DEFAULT '',
  matcher_score DOUBLE PRECISION NOT NULL DEFAULT 0,
  matcher_details JSONB NOT NULL DEFAULT '{}',
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'accepted', 'dismissed')),
  decided_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (agent_id, skill_id, action)
);

CREATE INDEX idx_agent_skill_suggestion_agent_pending
  ON agent_skill_suggestion(agent_id, status)
  WHERE status = 'pending';

CREATE INDEX idx_agent_skill_suggestion_workspace
  ON agent_skill_suggestion(workspace_id, agent_id);

DROP TABLE IF EXISTS evolution_unit_delivery;
