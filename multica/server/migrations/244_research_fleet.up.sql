-- Research Fleet: sealed research squad + sessions + exploration graph.

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_managed_role_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_managed_role_check
  CHECK (managed_role IS NULL OR managed_role IN ('group_manager', 'research_fleet'));

CREATE TABLE research_fleet (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  lead_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id)
);

CREATE TABLE research_fleet_member (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  fleet_id UUID NOT NULL REFERENCES research_fleet(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('pending_prompt_review', 'active', 'archived')),
  is_lead BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (fleet_id, agent_id)
);

CREATE INDEX research_fleet_member_workspace_idx
  ON research_fleet_member (workspace_id);
CREATE INDEX research_fleet_member_agent_idx
  ON research_fleet_member (agent_id);

CREATE TABLE research_session (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  fleet_id UUID NOT NULL REFERENCES research_fleet(id) ON DELETE CASCADE,
  created_by UUID NOT NULL REFERENCES "user"(id),
  title TEXT NOT NULL DEFAULT '',
  goal TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'drafting'
    CHECK (status IN ('drafting', 'running', 'awaiting_user_confirm', 'completed', 'archived')),
  current_stage TEXT NOT NULL DEFAULT 's1_plan'
    CHECK (current_stage IN ('s1_plan', 's2_sources', 's3_validation', 's4_delivery')),
  project_id UUID REFERENCES project(id) ON DELETE SET NULL,
  channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  handoff_summary TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX research_session_workspace_idx
  ON research_session (workspace_id, updated_at DESC);

CREATE TABLE research_graph_node (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  node_type TEXT NOT NULL
    CHECK (node_type IN (
      'goal', 'subquestion', 'probe', 'finding', 'conflict', 'dead_end',
      'refuted', 'pivot', 'roster_change', 'stage_gate', 'agent_activity'
    )),
  title TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'done', 'abandoned')),
  actor_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX research_graph_node_session_idx
  ON research_graph_node (session_id, created_at);

CREATE TABLE research_graph_edge (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  from_node_id UUID NOT NULL REFERENCES research_graph_node(id) ON DELETE CASCADE,
  to_node_id UUID NOT NULL REFERENCES research_graph_node(id) ON DELETE CASCADE,
  edge_type TEXT NOT NULL
    CHECK (edge_type IN ('leads_to', 'supports', 'contradicts', 'supersedes', 'abandons')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX research_graph_edge_session_idx
  ON research_graph_edge (session_id);

CREATE TABLE research_source (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  source_class TEXT NOT NULL DEFAULT 'other',
  credibility_weight DOUBLE PRECISION NOT NULL DEFAULT 0.5
    CHECK (credibility_weight >= 0 AND credibility_weight <= 1),
  stance TEXT NOT NULL DEFAULT '',
  relevance DOUBLE PRECISION NOT NULL DEFAULT 0.5
    CHECK (relevance >= 0 AND relevance <= 1),
  summary TEXT NOT NULL DEFAULT '',
  excerpt TEXT NOT NULL DEFAULT '',
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX research_source_session_idx
  ON research_source (session_id);

CREATE TABLE research_report (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  revision INT NOT NULL DEFAULT 1,
  content_md TEXT NOT NULL DEFAULT '',
  structured JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, revision)
);

CREATE TABLE research_stage_eval (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  stage TEXT NOT NULL
    CHECK (stage IN ('s1_plan', 's2_sources', 's3_validation', 's4_delivery')),
  passed BOOLEAN NOT NULL,
  score DOUBLE PRECISION NOT NULL DEFAULT 0,
  findings JSONB NOT NULL DEFAULT '[]'::jsonb,
  remediation TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX research_stage_eval_session_idx
  ON research_stage_eval (session_id, created_at DESC);

CREATE TABLE research_message (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  sender_type TEXT NOT NULL CHECK (sender_type IN ('user', 'agent', 'system')),
  sender_id UUID,
  target_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  body TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX research_message_session_idx
  ON research_message (session_id, created_at);

CREATE TABLE research_fleet_playbook (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  fleet_id UUID NOT NULL REFERENCES research_fleet(id) ON DELETE CASCADE,
  domain TEXT NOT NULL DEFAULT 'general',
  version INT NOT NULL DEFAULT 1,
  content_md TEXT NOT NULL DEFAULT '',
  research_fleet_only BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (fleet_id, domain, version)
);

CREATE TABLE research_fleet_feedback (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  fleet_id UUID NOT NULL REFERENCES research_fleet(id) ON DELETE CASCADE,
  session_id UUID REFERENCES research_session(id) ON DELETE SET NULL,
  stage TEXT,
  score DOUBLE PRECISION NOT NULL DEFAULT 0,
  notes TEXT NOT NULL DEFAULT '',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
