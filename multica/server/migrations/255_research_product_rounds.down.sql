ALTER TABLE research_graph_node
  DROP CONSTRAINT IF EXISTS research_graph_node_node_type_check;
ALTER TABLE research_graph_node
  ADD CONSTRAINT research_graph_node_node_type_check
  CHECK (node_type IN (
    'goal', 'subquestion', 'probe', 'finding', 'conflict', 'dead_end',
    'refuted', 'pivot', 'roster_change', 'stage_gate', 'agent_activity'
  ));

DROP INDEX IF EXISTS research_product_round_card_decided_by_agent_idx;
DROP TABLE IF EXISTS research_product_round_card;

ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_product_round_budget_check,
  DROP CONSTRAINT IF EXISTS research_session_product_round_check,
  DROP CONSTRAINT IF EXISTS research_session_depth_tier_check;

ALTER TABLE research_session
  DROP COLUMN IF EXISTS product_round_budget,
  DROP COLUMN IF EXISTS product_round,
  DROP COLUMN IF EXISTS depth_tier;
