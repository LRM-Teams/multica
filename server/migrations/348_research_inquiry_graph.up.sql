-- Chapter E foundation: canonical Inquiry Graph entities. V6 remains opt-in;
-- this migration creates durable state only and does not change Run defaults.

CREATE OR REPLACE FUNCTION research_artifact_entity_kind_allowed(kind TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT kind IN (
    'run_session', 'contract_revision', 'method_decision', 'question', 'task',
    'attempt', 'result_artifact', 'legacy_source', 'source_snapshot',
    'observation', 'claim', 'evidence_link', 'report_revision',
    'evaluation_decision', 'stage_evaluation', 'research_message',
    'product_round_decision', 'context_manifest', 'run_event', 'graph_node',
    'graph_edge', 'hypothesis', 'branch', 'insight', 'inquiry_edge'
  );
$$;

CREATE TABLE research_hypothesis (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL,
  session_id UUID NOT NULL, question_id UUID NOT NULL,
  statement TEXT NOT NULL CHECK (char_length(statement) BETWEEN 1 AND 32768),
  applicability JSONB NOT NULL DEFAULT '{}'::jsonb,
  expected_observations JSONB NOT NULL DEFAULT '[]'::jsonb,
  weakening_conditions JSONB NOT NULL DEFAULT '[]'::jsonb,
  status TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed','investigating','supported','weakened','refuted','conditional','obsolete')),
  confidence_low DOUBLE PRECISION CHECK (confidence_low BETWEEN 0 AND 1),
  confidence_high DOUBLE PRECISION CHECK (confidence_high BETWEEN 0 AND 1),
  created_by_task_id UUID, created_by_attempt_id UUID, last_evaluated_state_version BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, session_id, id),
  CONSTRAINT research_hypothesis_confidence_order CHECK (confidence_low IS NULL OR confidence_high IS NULL OR confidence_low <= confidence_high),
  CONSTRAINT research_hypothesis_session_fk FOREIGN KEY (workspace_id, session_id) REFERENCES research_session(workspace_id,id),
  CONSTRAINT research_hypothesis_question_fk FOREIGN KEY (workspace_id,session_id,question_id) REFERENCES research_question(workspace_id,session_id,id),
  CONSTRAINT research_hypothesis_task_fk FOREIGN KEY (workspace_id,session_id,created_by_task_id) REFERENCES research_task(workspace_id,session_id,id),
  CONSTRAINT research_hypothesis_attempt_fk FOREIGN KEY (workspace_id,session_id,created_by_attempt_id) REFERENCES research_task_attempt(workspace_id,session_id,id)
);

CREATE TABLE research_branch (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL,
  session_id UUID NOT NULL, parent_branch_id UUID,
  objective TEXT NOT NULL CHECK (char_length(objective) BETWEEN 1 AND 32768),
  entry_conditions JSONB NOT NULL DEFAULT '[]'::jsonb,
  exit_conditions JSONB NOT NULL DEFAULT '[]'::jsonb,
  budget_share DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (budget_share BETWEEN 0 AND 1),
  status TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed','active','paused','completed','terminated','obsolete')),
  termination_reason TEXT NOT NULL DEFAULT '', created_by_task_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  CONSTRAINT research_branch_session_fk FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id),
  CONSTRAINT research_branch_parent_fk FOREIGN KEY (workspace_id,session_id,parent_branch_id) REFERENCES research_branch(workspace_id,session_id,id),
  CONSTRAINT research_branch_task_fk FOREIGN KEY (workspace_id,session_id,created_by_task_id) REFERENCES research_task(workspace_id,session_id,id)
);

CREATE TABLE research_insight (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL,
  session_id UUID NOT NULL, title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 4096),
  summary TEXT NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 32768),
  status TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed','accepted','stale','superseded','obsolete')),
  importance DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (importance BETWEEN 0 AND 1),
  level INTEGER NOT NULL DEFAULT 1 CHECK (level >= 1), created_by_attempt_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  CONSTRAINT research_insight_session_fk FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id),
  CONSTRAINT research_insight_attempt_fk FOREIGN KEY (workspace_id,session_id,created_by_attempt_id) REFERENCES research_task_attempt(workspace_id,session_id,id)
);

CREATE TABLE research_inquiry_edge (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL,
  session_id UUID NOT NULL, from_kind TEXT NOT NULL, from_entity_id UUID NOT NULL,
  to_kind TEXT NOT NULL, to_entity_id UUID NOT NULL,
  relation TEXT NOT NULL CHECK (relation IN ('decomposes','tests','explains','depends_on','competes_with','refines','invalidates','motivates')),
  rationale TEXT NOT NULL DEFAULT '', created_by_attempt_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,from_kind,from_entity_id,to_kind,to_entity_id,relation),
  CONSTRAINT research_inquiry_edge_distinct CHECK (from_kind <> to_kind OR from_entity_id <> to_entity_id),
  CONSTRAINT research_inquiry_edge_kind CHECK (from_kind IN ('question','hypothesis','branch','claim','insight','dispute') AND to_kind IN ('question','hypothesis','branch','claim','insight','dispute')),
  CONSTRAINT research_inquiry_edge_session_fk FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id),
  CONSTRAINT research_inquiry_edge_attempt_fk FOREIGN KEY (workspace_id,session_id,created_by_attempt_id) REFERENCES research_task_attempt(workspace_id,session_id,id)
);

CREATE INDEX research_hypothesis_frontier_idx ON research_hypothesis(session_id,status,updated_at,id);
CREATE INDEX research_branch_frontier_idx ON research_branch(session_id,status,updated_at,id);
CREATE INDEX research_insight_status_idx ON research_insight(session_id,status,level,id);
CREATE INDEX research_inquiry_edge_from_idx ON research_inquiry_edge(session_id,from_kind,from_entity_id,relation);
CREATE INDEX research_inquiry_edge_to_idx ON research_inquiry_edge(session_id,to_kind,to_entity_id,relation);
