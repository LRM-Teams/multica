-- Canonical executable Work Graphs. Existing Wendy work_node/work_edge tables
-- remain an operational projection until their callers migrate to this model.
CREATE TABLE work_graph (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  anchor_kind TEXT NOT NULL CHECK (anchor_kind IN ('channel_goal', 'issue', 'research_run')),
  anchor_id UUID NOT NULL,
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'paused', 'deliverable', 'completed', 'cancelled', 'failed')),
  current_version BIGINT NOT NULL DEFAULT 1 CHECK (current_version > 0),
  admission_decision TEXT NOT NULL CHECK (admission_decision IN ('GRAPH', 'PROPOSE_GRAPH')),
  budget_policy JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(budget_policy) = 'object'),
  created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member', 'agent', 'system')),
  created_by_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, anchor_kind, anchor_id)
);

CREATE TABLE work_graph_revision (
  graph_id UUID NOT NULL REFERENCES work_graph(id) ON DELETE CASCADE,
  version BIGINT NOT NULL CHECK (version > 0),
  previous_version BIGINT,
  reason TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 4000),
  author_type TEXT NOT NULL CHECK (author_type IN ('member', 'agent', 'system')),
  author_id UUID,
  topology_digest TEXT NOT NULL,
  expected_cost_delta JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(expected_cost_delta) = 'object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (graph_id, version)
);

CREATE TABLE work_graph_node (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  graph_id UUID NOT NULL REFERENCES work_graph(id) ON DELETE CASCADE,
  issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  parent_node_id UUID REFERENCES work_graph_node(id) ON DELETE SET NULL,
  role TEXT NOT NULL CHECK (role IN ('planner','worker','explorer','critic','verifier','replicator','judge','synthesizer','promoter','observer')),
  context_policy TEXT NOT NULL DEFAULT 'bounded'
    CHECK (context_policy IN ('full','bounded','blind','adversarial','replication','sealed')),
  execution_status TEXT NOT NULL DEFAULT 'draft'
    CHECK (execution_status IN ('draft','queued','ready','running','waiting','succeeded','failed','cancelled')),
  validity_status TEXT NOT NULL DEFAULT 'valid'
    CHECK (validity_status IN ('valid','stale','invalidated','superseded')),
  review_status TEXT NOT NULL DEFAULT 'unreviewed'
    CHECK (review_status IN ('unreviewed','reviewing','accepted','rejected','blocked')),
  objective TEXT NOT NULL DEFAULT '',
  completion_contract JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(completion_contract) = 'array'),
  input_revision_digest TEXT NOT NULL DEFAULT '',
  depth INTEGER NOT NULL DEFAULT 0 CHECK (depth >= 0),
  attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
  budget JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(budget) = 'object'),
  based_on_graph_version BIGINT NOT NULL CHECK (based_on_graph_version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (graph_id, issue_id),
  UNIQUE (workspace_id, id)
);

CREATE TABLE work_graph_edge (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  graph_id UUID NOT NULL REFERENCES work_graph(id) ON DELETE CASCADE,
  from_node_id UUID NOT NULL REFERENCES work_graph_node(id) ON DELETE CASCADE,
  to_node_id UUID NOT NULL REFERENCES work_graph_node(id) ON DELETE CASCADE,
  edge_type TEXT NOT NULL CHECK (edge_type IN ('contains','depends_on','verifies','critiques','replicates','synthesizes','supersedes','promotes')),
  required BOOLEAN NOT NULL DEFAULT true,
  created_version BIGINT NOT NULL CHECK (created_version > 0),
  retired_version BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (from_node_id <> to_node_id),
  CHECK (retired_version IS NULL OR retired_version >= created_version),
  UNIQUE (graph_id, from_node_id, to_node_id, edge_type, created_version)
);

CREATE INDEX work_graph_node_ready_idx ON work_graph_node (graph_id, execution_status, created_at);
CREATE INDEX work_graph_edge_to_active_idx ON work_graph_edge (graph_id, to_node_id) WHERE retired_version IS NULL;
CREATE INDEX work_graph_edge_from_active_idx ON work_graph_edge (graph_id, from_node_id) WHERE retired_version IS NULL;

CREATE TABLE work_artifact_revision (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  graph_id UUID NOT NULL REFERENCES work_graph(id) ON DELETE CASCADE,
  artifact_id UUID NOT NULL DEFAULT gen_random_uuid(),
  producer_node_id UUID NOT NULL REFERENCES work_graph_node(id) ON DELETE CASCADE,
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  digest TEXT NOT NULL CHECK (length(btrim(digest)) > 0),
  kind TEXT NOT NULL CHECK (length(btrim(kind)) > 0),
  locator TEXT NOT NULL CHECK (length(btrim(locator)) > 0),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
  validity_status TEXT NOT NULL DEFAULT 'valid'
    CHECK (validity_status IN ('valid','stale','invalidated','superseded')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (artifact_id, revision),
  UNIQUE (graph_id, digest)
);

CREATE TABLE work_verification_attempt (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  graph_id UUID NOT NULL REFERENCES work_graph(id) ON DELETE CASCADE,
  verifier_node_id UUID NOT NULL REFERENCES work_graph_node(id) ON DELETE CASCADE,
  subject_artifact_revision_id UUID NOT NULL REFERENCES work_artifact_revision(id) ON DELETE CASCADE,
  scope_digest TEXT NOT NULL CHECK (length(btrim(scope_digest)) > 0),
  verdict TEXT NOT NULL CHECK (verdict IN ('PASS','FAIL','BLOCKED')),
  findings JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(findings) = 'array'),
  evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_refs) = 'array'),
  stale_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX work_verification_active_scope_idx
  ON work_verification_attempt (subject_artifact_revision_id, scope_digest)
  WHERE stale_at IS NULL;

CREATE TABLE work_graph_change_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  graph_id UUID NOT NULL REFERENCES work_graph(id) ON DELETE CASCADE,
  version BIGINT NOT NULL,
  event_type TEXT NOT NULL,
  affected_nodes UUID[] NOT NULL DEFAULT '{}',
  reason TEXT NOT NULL DEFAULT '',
  payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX work_graph_change_event_delta_idx ON work_graph_change_event (graph_id, version, created_at, id);

CREATE TABLE work_graph_create_request (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  actor_type TEXT NOT NULL CHECK (actor_type IN ('member','agent')),
  actor_id UUID NOT NULL,
  idempotency_key UUID NOT NULL,
  request_digest TEXT NOT NULL,
  graph_id UUID NOT NULL REFERENCES work_graph(id) ON DELETE CASCADE,
  response JSONB NOT NULL CHECK (jsonb_typeof(response) = 'object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, actor_type, actor_id, idempotency_key)
);
