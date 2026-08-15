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
    'research_director_identity'
  );
$$;

CREATE TABLE research_director_identity (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  identity_version INTEGER NOT NULL CHECK (identity_version >= 1), agent_id UUID NOT NULL,
  assigned_by_user_id UUID NOT NULL, reason TEXT NOT NULL CHECK (length(btrim(reason)) > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (workspace_id,session_id,id),
  UNIQUE (workspace_id,session_id,identity_version),
  CONSTRAINT research_director_identity_session_fk FOREIGN KEY (workspace_id,session_id)
    REFERENCES research_session(workspace_id,id) ON DELETE CASCADE
);
CREATE INDEX research_director_identity_current_idx
  ON research_director_identity(workspace_id,session_id,identity_version DESC);

CREATE OR REPLACE FUNCTION research_director_identity_passport_class_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM research_director_identity entity
    WHERE (entity.workspace_id,entity.session_id,entity.id)=(NEW.workspace_id,NEW.session_id,NEW.id)
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard';
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER research_artifact_passport_class_guard ON research_artifact_passport;
CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind NOT IN ('hypothesis','branch','insight','inquiry_edge','research_director_identity'))
EXECUTE FUNCTION research_artifact_passport_class_guard_fn();
CREATE CONSTRAINT TRIGGER research_director_identity_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind = 'research_director_identity')
EXECUTE FUNCTION research_director_identity_passport_class_guard_fn();

CREATE CONSTRAINT TRIGGER research_director_identity_artifact_passport_guard
AFTER INSERT OR UPDATE OF id,workspace_id,session_id ON research_director_identity
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('research_director_identity');
CREATE TRIGGER research_director_identity_artifact_passport_delete_guard
BEFORE DELETE ON research_director_identity FOR EACH ROW
EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('research_director_identity');
