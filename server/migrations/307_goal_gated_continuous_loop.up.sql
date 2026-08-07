-- Goal Gate: executable graph completion is owned by kernel evidence, while
-- pre-cutover non-Goal graphs retain their legacy Issue-driven behavior.
ALTER TABLE work_graph_node
  ADD COLUMN completion_authority TEXT NOT NULL DEFAULT 'kernel_evidence'
    CHECK (completion_authority IN ('issue_status', 'kernel_evidence')),
  ADD COLUMN effective_completion TEXT NOT NULL DEFAULT 'pending'
    CHECK (effective_completion IN ('pending', 'satisfied', 'stale', 'revoked'));

UPDATE work_graph_node node
SET completion_authority = CASE
      WHEN graph.anchor_kind = 'channel_goal' THEN 'kernel_evidence'
      ELSE 'issue_status'
    END,
    effective_completion = CASE
      WHEN graph.anchor_kind <> 'channel_goal'
       AND node.execution_status = 'succeeded'
       AND node.validity_status = 'valid' THEN 'satisfied'
      WHEN node.validity_status IN ('stale', 'superseded') THEN 'stale'
      WHEN node.validity_status = 'invalidated' THEN 'revoked'
      ELSE 'pending'
    END
FROM work_graph graph
WHERE graph.id = node.graph_id;

CREATE INDEX work_graph_node_frontier_idx
  ON work_graph_node (graph_id, effective_completion, execution_status, created_at);

-- Continuous Goals advance through bounded, auditable epochs. A Goal may be
-- logically continuous, but every physical execution interval owns one lease
-- and one terminal decision.
CREATE TABLE goal_execution_epoch (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  goal_id UUID NOT NULL REFERENCES channel_goal(id) ON DELETE CASCADE,
  graph_id UUID NOT NULL REFERENCES work_graph(id) ON DELETE CASCADE,
  epoch_number BIGINT NOT NULL CHECK (epoch_number > 0),
  status TEXT NOT NULL DEFAULT 'planned'
    CHECK (status IN ('planned','running','evaluating','committed','waiting','stopped','cancelled')),
  contract JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(contract) = 'object'),
  budget JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(budget) = 'object'),
  evaluation JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evaluation) = 'object'),
  decision TEXT CHECK (decision IS NULL OR decision IN
    ('CONTINUE','WAIT','ASK_HUMAN','RETRY_OPERATION','REPAIR_CONTRACT','REPLAN_NEW_AXIS',
     'STOP_CONVERGED','STOP_NO_GAIN','STOP_BUDGET')),
  lease_owner UUID,
  lease_token UUID,
  lease_expires_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ,
  committed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (goal_id, epoch_number),
  UNIQUE (graph_id, epoch_number)
);

CREATE UNIQUE INDEX goal_execution_epoch_one_live
  ON goal_execution_epoch(goal_id)
  WHERE status IN ('planned','running','evaluating','waiting');

CREATE INDEX goal_execution_epoch_lease
  ON goal_execution_epoch(status, lease_expires_at)
  WHERE status IN ('running','evaluating');

CREATE TABLE issue_decompose_request (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  parent_issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  actor_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  idempotency_key UUID NOT NULL,
  request_digest TEXT NOT NULL,
  response JSONB NOT NULL CHECK (jsonb_typeof(response) = 'object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, actor_agent_id, idempotency_key)
);

-- Required both for FK enforcement performance and the recursive Agent
-- hard-delete closure (actor_agent_id is not the leading PK column).
CREATE INDEX issue_decompose_request_actor_agent_idx
  ON issue_decompose_request(actor_agent_id);

-- Optional strong-isolation workers for ordinary Issue DAG nodes. The source
-- Agent remains the user-facing identity; the derived Agent owns exactly one
-- child Issue and is archived when that Issue becomes done or cancelled.
CREATE TABLE issue_derived_agent_assignment (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  parent_issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  issue_id UUID NOT NULL UNIQUE REFERENCES issue(id) ON DELETE CASCADE,
  source_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE RESTRICT,
  derived_agent_id UUID NOT NULL UNIQUE REFERENCES agent(id) ON DELETE CASCADE,
  memory_policy TEXT NOT NULL DEFAULT 'snapshot_readonly_source'
    CHECK (memory_policy IN ('snapshot_readonly_source')),
  lifecycle_policy TEXT NOT NULL DEFAULT 'archive_on_issue_terminal'
    CHECK (lifecycle_policy IN ('archive_on_issue_terminal')),
  clone_reason TEXT NOT NULL CHECK (length(btrim(clone_reason)) BETWEEN 1 AND 1000),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX issue_derived_agent_assignment_source_idx
  ON issue_derived_agent_assignment(source_agent_id);
CREATE INDEX issue_derived_agent_assignment_derived_idx
  ON issue_derived_agent_assignment(derived_agent_id);
CREATE INDEX issue_derived_agent_assignment_parent_idx
  ON issue_derived_agent_assignment(parent_issue_id);
