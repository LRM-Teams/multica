-- Per-agent downflow queue for governed evolution units.

CREATE TABLE evolution_unit_delivery (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  unit_id UUID NOT NULL REFERENCES shared_evolution_unit(id) ON DELETE CASCADE,
  version_id UUID REFERENCES shared_evolution_unit_version(id) ON DELETE SET NULL,
  target_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,

  delivery_type TEXT NOT NULL DEFAULT 'inbox'
    CHECK (delivery_type IN ('recommendation', 'inbox', 'generated', 'shared_cache')),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'delivered', 'accepted', 'ignored', 'rejected', 'failed', 'withdrawn')),

  reason TEXT NOT NULL DEFAULT '',
  matcher_score DOUBLE PRECISION NOT NULL DEFAULT 0,
  matcher_details JSONB NOT NULL DEFAULT '{}',

  delivered_path TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',

  decided_at TIMESTAMPTZ,
  delivered_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

  UNIQUE(unit_id, version_id, target_agent_id)
);

CREATE INDEX idx_evolution_delivery_agent_status
  ON evolution_unit_delivery(target_agent_id, status, created_at DESC);

CREATE INDEX idx_evolution_delivery_workspace_status
  ON evolution_unit_delivery(workspace_id, status, created_at DESC);
