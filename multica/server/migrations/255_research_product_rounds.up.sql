-- LRM-911: product rounds (Round N) + end-of-round judgment cards.
-- Product rounds are distinct from fleet S1–S4 stages and from single probe/tool steps.
-- Hard caps align LRM-676 depth tiers: shallow=2 / standard=5 / deep=10.

ALTER TABLE research_session
  ADD COLUMN IF NOT EXISTS depth_tier TEXT NOT NULL DEFAULT 'standard',
  ADD COLUMN IF NOT EXISTS product_round INTEGER NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS product_round_budget INTEGER NOT NULL DEFAULT 5;

ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_depth_tier_check;
ALTER TABLE research_session
  ADD CONSTRAINT research_session_depth_tier_check
  CHECK (depth_tier IN ('shallow', 'standard', 'deep'));

ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_product_round_check;
ALTER TABLE research_session
  ADD CONSTRAINT research_session_product_round_check
  CHECK (product_round >= 1);

ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_product_round_budget_check;
ALTER TABLE research_session
  ADD CONSTRAINT research_session_product_round_budget_check
  CHECK (product_round_budget IN (2, 5, 10));

UPDATE research_session SET
  product_round_budget = CASE depth_tier
    WHEN 'shallow' THEN 2
    WHEN 'deep' THEN 10
    ELSE 5
  END
WHERE product_round_budget NOT IN (2, 5, 10)
   OR (depth_tier = 'shallow' AND product_round_budget <> 2)
   OR (depth_tier = 'deep' AND product_round_budget <> 10)
   OR (depth_tier = 'standard' AND product_round_budget <> 5);

CREATE TABLE research_product_round_card (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  round_number INTEGER NOT NULL CHECK (round_number >= 1),
  decision TEXT NOT NULL
    CHECK (decision IN ('continue', 'stop_enough', 'stop_budget')),
  coverage_gaps JSONB NOT NULL DEFAULT '[]'::jsonb,
  confidence_note TEXT NOT NULL DEFAULT '',
  budget_used INTEGER NOT NULL CHECK (budget_used >= 0),
  budget_remaining INTEGER NOT NULL CHECK (budget_remaining >= 0),
  goal_patch_proposal TEXT,
  next_round_focus TEXT,
  decided_by_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, round_number)
);

CREATE INDEX research_product_round_card_session_idx
  ON research_product_round_card (session_id, round_number DESC);

-- Supporting index for agent hard-delete / ON DELETE SET NULL on decided_by_agent_id.
CREATE INDEX research_product_round_card_decided_by_agent_idx
  ON research_product_round_card (decided_by_agent_id)
  WHERE decided_by_agent_id IS NOT NULL;

-- Optional canvas node for judgment cards (distinct from S1–S4 stage_gate).
ALTER TABLE research_graph_node
  DROP CONSTRAINT IF EXISTS research_graph_node_node_type_check;
ALTER TABLE research_graph_node
  ADD CONSTRAINT research_graph_node_node_type_check
  CHECK (node_type IN (
    'goal', 'subquestion', 'probe', 'finding', 'conflict', 'dead_end',
    'refuted', 'pivot', 'roster_change', 'stage_gate', 'agent_activity',
    'product_round_gate'
  ));
