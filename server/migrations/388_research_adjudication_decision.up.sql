CREATE OR REPLACE FUNCTION research_artifact_entity_kind_allowed(kind TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT kind IN (
    'run_session', 'contract_revision', 'method_decision', 'question', 'task',
    'attempt', 'result_artifact', 'legacy_source', 'source_snapshot',
    'observation', 'claim', 'evidence_link', 'report_revision',
    'evaluation_decision', 'stage_evaluation', 'research_message',
    'product_round_decision', 'context_manifest', 'run_event', 'graph_node',
    'graph_edge', 'hypothesis', 'branch', 'insight', 'inquiry_edge',
    'integration_round', 'integration_contribution', 'insight_derivation',
    'dispute', 'dispute_position', 'deliberation', 'deliberation_turn',
    'search_plan', 'query_execution', 'source_candidate', 'screening_decision',
    'research_director_identity', 'adjudication_decision'
  );
$$;

CREATE TABLE research_adjudication_decision (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  dispute_id UUID NOT NULL, deliberation_id UUID NOT NULL, task_id UUID NOT NULL,
  attempt_id UUID NOT NULL, director_identity_id UUID NOT NULL,
  director_identity_version INTEGER NOT NULL CHECK (director_identity_version >= 1),
  decision TEXT NOT NULL CHECK (decision IN ('resolved','conditionally_resolved','irreducible')),
  rationale TEXT NOT NULL CHECK (length(btrim(rationale)) > 0),
  conditions JSONB NOT NULL CHECK (jsonb_typeof(conditions)='array'),
  residual_uncertainty TEXT NOT NULL DEFAULT '',
  position_assessments JSONB NOT NULL CHECK (jsonb_typeof(position_assessments)='array' AND jsonb_array_length(position_assessments)>0),
  evidence_ids JSONB NOT NULL CHECK (jsonb_typeof(evidence_ids)='array' AND jsonb_array_length(evidence_ids)>0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id),
  UNIQUE(workspace_id,session_id,dispute_id,director_identity_version),
  FOREIGN KEY(workspace_id,session_id) REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
  FOREIGN KEY(workspace_id,session_id,dispute_id) REFERENCES research_dispute(workspace_id,session_id,id),
  FOREIGN KEY(workspace_id,session_id,deliberation_id) REFERENCES research_deliberation(workspace_id,session_id,id),
  FOREIGN KEY(workspace_id,session_id,task_id) REFERENCES research_task(workspace_id,session_id,id),
  FOREIGN KEY(workspace_id,session_id,attempt_id) REFERENCES research_task_attempt(workspace_id,session_id,id),
  FOREIGN KEY(director_identity_id) REFERENCES research_director_identity(id)
);
