-- Canonical Integration and Dispute graph foundation. V6 remains opt-in.

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
    'dispute', 'dispute_position', 'deliberation', 'deliberation_turn'
  );
$$;

CREATE TABLE research_integration_round (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('result_batch','high_impact_change','catch_up','manual')),
  input_event_sequence BIGINT NOT NULL CHECK (input_event_sequence >= 0),
  input_state_hash TEXT NOT NULL CHECK (input_state_hash ~ '^sha256:[0-9a-f]{64}$'),
  input_artifact_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(input_artifact_ids) = 'array'),
  goal_version INTEGER NOT NULL CHECK (goal_version >= 1),
  plan_version INTEGER NOT NULL CHECK (plan_version >= 1),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','partially_accepted','accepted','superseded','failed')),
  rejection_results JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(rejection_results) = 'array'),
  created_by_task_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,input_event_sequence,input_state_hash),
  CONSTRAINT research_integration_round_session_fk FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id),
  CONSTRAINT research_integration_round_task_fk FOREIGN KEY (workspace_id,session_id,created_by_task_id) REFERENCES research_task(workspace_id,session_id,id)
);

CREATE TABLE research_integration_contribution (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  integration_round_id UUID NOT NULL,
  author_agent_id UUID NOT NULL,
  compared_artifact_ids JSONB NOT NULL CHECK (jsonb_typeof(compared_artifact_ids) = 'array' AND jsonb_array_length(compared_artifact_ids) > 0),
  common_findings JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(common_findings) = 'array'),
  unique_findings JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(unique_findings) = 'array'),
  conflicts JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(conflicts) = 'array'),
  scope JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(scope) = 'object'),
  omissions JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(omissions) = 'array'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,integration_round_id,author_agent_id),
  CONSTRAINT research_integration_contribution_round_fk FOREIGN KEY (workspace_id,session_id,integration_round_id) REFERENCES research_integration_round(workspace_id,session_id,id),
  CONSTRAINT research_integration_contribution_session_fk FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id)
);

CREATE TABLE research_insight_derivation (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  insight_id UUID NOT NULL,
  input_kind TEXT NOT NULL CHECK (input_kind IN ('claim','insight')),
  input_entity_id UUID NOT NULL,
  input_content_hash TEXT NOT NULL CHECK (input_content_hash ~ '^sha256:[0-9a-f]{64}$'),
  scope_hash TEXT NOT NULL CHECK (scope_hash ~ '^sha256:[0-9a-f]{64}$'),
  relation TEXT NOT NULL CHECK (relation IN ('integrates','explains','conditions','resolves','distinguishes')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,insight_id,input_kind,input_entity_id),
  CONSTRAINT research_insight_derivation_session_fk FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id),
  CONSTRAINT research_insight_derivation_output_fk FOREIGN KEY (workspace_id,session_id,insight_id) REFERENCES research_insight(workspace_id,session_id,id)
);

CREATE FUNCTION research_validate_insight_derivation_input() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.input_kind = 'claim' AND NOT EXISTS (
    SELECT 1 FROM research_claim WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.input_entity_id
  ) THEN RAISE EXCEPTION 'insight derivation Claim input is outside the Run';
  ELSIF NEW.input_kind = 'insight' AND NOT EXISTS (
    SELECT 1 FROM research_insight WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.input_entity_id
  ) THEN RAISE EXCEPTION 'insight derivation Insight input is outside the Run';
  END IF;
  IF NEW.input_kind = 'insight' AND NEW.input_entity_id = NEW.insight_id THEN
    RAISE EXCEPTION 'Insight cannot derive from itself';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER research_insight_derivation_input_guard BEFORE INSERT OR UPDATE ON research_insight_derivation
FOR EACH ROW EXECUTE FUNCTION research_validate_insight_derivation_input();

CREATE TABLE research_dispute (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  subject_kind TEXT NOT NULL CHECK (subject_kind IN ('question','hypothesis','claim','insight')),
  subject_entity_id UUID NOT NULL, dispute_kind TEXT NOT NULL CHECK (dispute_kind IN ('logical','source_interpretation','version','unit','scope','method','semantic')),
  severity TEXT NOT NULL CHECK (severity IN ('advisory','blocking')), materiality DOUBLE PRECISION NOT NULL CHECK (materiality BETWEEN 0 AND 1),
  status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','investigating','resolved','conditionally_resolved','irreducible','obsolete')),
  resolution_request TEXT NOT NULL CHECK (length(btrim(resolution_request)) > 0), resolution_explanation TEXT NOT NULL DEFAULT '', residual_uncertainty TEXT NOT NULL DEFAULT '',
  created_by_attempt_id UUID, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,session_id,id), CONSTRAINT research_dispute_session_fk FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id),
  CONSTRAINT research_dispute_attempt_fk FOREIGN KEY (workspace_id,session_id,created_by_attempt_id) REFERENCES research_task_attempt(workspace_id,session_id,id)
);

CREATE FUNCTION research_validate_dispute_subject() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.subject_kind = 'question' AND NOT EXISTS (SELECT 1 FROM research_question WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.subject_entity_id) THEN
    RAISE EXCEPTION 'Dispute Question subject is outside the Run';
  ELSIF NEW.subject_kind = 'hypothesis' AND NOT EXISTS (SELECT 1 FROM research_hypothesis WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.subject_entity_id) THEN
    RAISE EXCEPTION 'Dispute Hypothesis subject is outside the Run';
  ELSIF NEW.subject_kind = 'claim' AND NOT EXISTS (SELECT 1 FROM research_claim WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.subject_entity_id) THEN
    RAISE EXCEPTION 'Dispute Claim subject is outside the Run';
  ELSIF NEW.subject_kind = 'insight' AND NOT EXISTS (SELECT 1 FROM research_insight WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.subject_entity_id) THEN
    RAISE EXCEPTION 'Dispute Insight subject is outside the Run';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER research_dispute_subject_guard BEFORE INSERT OR UPDATE ON research_dispute
FOR EACH ROW EXECUTE FUNCTION research_validate_dispute_subject();

CREATE TABLE research_dispute_position (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL, dispute_id UUID NOT NULL,
  author_agent_id UUID NOT NULL, statement TEXT NOT NULL CHECK (length(btrim(statement)) > 0), scope JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(scope)='object'),
  claim_ids JSONB NOT NULL CHECK (jsonb_typeof(claim_ids)='array' AND jsonb_array_length(claim_ids)>0), evidence_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_ids)='array'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (workspace_id,session_id,id),
  CONSTRAINT research_dispute_position_dispute_fk FOREIGN KEY (workspace_id,session_id,dispute_id) REFERENCES research_dispute(workspace_id,session_id,id),
  CONSTRAINT research_dispute_position_session_fk FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id)
);

CREATE TABLE research_deliberation (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL, dispute_id UUID NOT NULL,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','consensus_proposed','awaiting_external_evidence','deadlocked','escalated','completed')),
  round_count INTEGER NOT NULL DEFAULT 0 CHECK (round_count>=0), no_progress_rounds INTEGER NOT NULL DEFAULT 0 CHECK (no_progress_rounds>=0),
  director_agent_id UUID NOT NULL, canonical_watermark JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(canonical_watermark)='object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (workspace_id,session_id,id), UNIQUE (workspace_id,session_id,dispute_id),
  CONSTRAINT research_deliberation_dispute_fk FOREIGN KEY (workspace_id,session_id,dispute_id) REFERENCES research_dispute(workspace_id,session_id,id)
);

CREATE TABLE research_deliberation_turn (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL, deliberation_id UUID NOT NULL,
  round_number INTEGER NOT NULL CHECK (round_number>=1), actor_agent_id UUID NOT NULL, position_id UUID,
  evidence_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(evidence_ids)='array'), challenge TEXT NOT NULL DEFAULT '', concession TEXT NOT NULL DEFAULT '',
  proposed_action JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(proposed_action)='object'), canonical_delta JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(canonical_delta)='object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (workspace_id,session_id,id), UNIQUE (workspace_id,session_id,deliberation_id,round_number,actor_agent_id),
  CONSTRAINT research_deliberation_turn_deliberation_fk FOREIGN KEY (workspace_id,session_id,deliberation_id) REFERENCES research_deliberation(workspace_id,session_id,id),
  CONSTRAINT research_deliberation_turn_position_fk FOREIGN KEY (workspace_id,session_id,position_id) REFERENCES research_dispute_position(workspace_id,session_id,id)
);

CREATE INDEX research_integration_round_status_idx ON research_integration_round(session_id,status,input_event_sequence);
CREATE INDEX research_insight_derivation_input_idx ON research_insight_derivation(session_id,input_kind,input_entity_id);
CREATE INDEX research_dispute_status_idx ON research_dispute(session_id,severity,status,materiality DESC);
CREATE INDEX research_deliberation_status_idx ON research_deliberation(session_id,status,updated_at);
