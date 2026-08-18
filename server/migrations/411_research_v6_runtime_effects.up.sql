CREATE TABLE research_v6_runtime_effect (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  effect_kind TEXT NOT NULL CHECK (effect_kind IN ('create_agent')),
  idempotency_key TEXT NOT NULL,
  resource_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id,effect_kind,idempotency_key),
  UNIQUE (workspace_id,effect_kind,resource_id)
);

COMMENT ON TABLE research_v6_runtime_effect IS
  'Durable idempotency receipts for Ronaldo V6 effects executed outside the canonical research graph.';
