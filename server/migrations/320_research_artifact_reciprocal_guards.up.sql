-- Chapter D1c: reciprocal deferred domain↔passport guards (design §4.7.2–3).

CREATE OR REPLACE FUNCTION research_artifact_session_still_exists(
  p_workspace_id UUID,
  p_session_id UUID
)
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
  SELECT EXISTS (
    SELECT 1 FROM research_session s
    WHERE s.workspace_id = p_workspace_id AND s.id = p_session_id
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_require_matching_passport(
  p_kind TEXT,
  p_workspace_id UUID,
  p_session_id UUID,
  p_entity_id UUID
)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_artifact_entity_kind_allowed(p_kind) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_domain_passport_guard';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_passport p
    WHERE p.workspace_id = p_workspace_id
      AND p.session_id = p_session_id
      AND p.id = p_entity_id
      AND p.entity_kind = p_kind
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_domain_passport_guard';
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_domain_passport_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM research_artifact_require_matching_passport(
    TG_ARGV[0], NEW.workspace_id, NEW.session_id, NEW.id
  );
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_domain_passport_delete_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_artifact_session_still_exists(OLD.workspace_id, OLD.session_id) THEN
    RETURN OLD;
  END IF;
  IF EXISTS (
    SELECT 1 FROM research_artifact_passport p
    WHERE p.workspace_id = OLD.workspace_id
      AND p.session_id = OLD.session_id
      AND p.id = OLD.id
      AND p.entity_kind = TG_ARGV[0]
  ) THEN
    RAISE EXCEPTION 'research domain row cannot be deleted while artifact passport exists'
      USING ERRCODE = '55000', CONSTRAINT = TG_NAME;
  END IF;
  RETURN OLD;
END;
$$;

CREATE OR REPLACE FUNCTION research_session_artifact_passport_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM research_artifact_require_matching_passport(
    'run_session', NEW.workspace_id, NEW.id, NEW.id
  );
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION research_session_artifact_passport_delete_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  -- Session rows are the session anchor; deleting them cascades passports.
  RETURN OLD;
END;
$$;

CREATE OR REPLACE FUNCTION research_decision_artifact_passport_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_kind TEXT;
BEGIN
  IF NEW.decision_kind = 'research_method' THEN
    v_kind := 'method_decision';
  ELSE
    v_kind := 'evaluation_decision';
  END IF;
  PERFORM research_artifact_require_matching_passport(
    v_kind, NEW.workspace_id, NEW.session_id, NEW.id
  );
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION research_decision_artifact_passport_delete_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_kind TEXT;
BEGIN
  IF NOT research_artifact_session_still_exists(OLD.workspace_id, OLD.session_id) THEN
    RETURN OLD;
  END IF;
  IF OLD.decision_kind = 'research_method' THEN
    v_kind := 'method_decision';
  ELSE
    v_kind := 'evaluation_decision';
  END IF;
  IF EXISTS (
    SELECT 1 FROM research_artifact_passport p
    WHERE p.workspace_id = OLD.workspace_id
      AND p.session_id = OLD.session_id
      AND p.id = OLD.id
      AND p.entity_kind = v_kind
  ) THEN
    RAISE EXCEPTION 'research domain row cannot be deleted while artifact passport exists'
      USING ERRCODE = '55000', CONSTRAINT = 'research_decision_artifact_passport_delete_guard';
  END IF;
  RETURN OLD;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_passport_delete_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_artifact_session_still_exists(OLD.workspace_id, OLD.session_id) THEN
    RETURN OLD;
  END IF;
  CASE OLD.entity_kind
    WHEN 'run_session' THEN
      IF EXISTS (
        SELECT 1 FROM research_session s
        WHERE s.workspace_id = OLD.workspace_id AND s.id = OLD.id AND s.id = OLD.session_id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'contract_revision' THEN
      IF EXISTS (
        SELECT 1 FROM research_contract_revision r
        WHERE r.workspace_id = OLD.workspace_id AND r.session_id = OLD.session_id AND r.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'method_decision' THEN
      IF EXISTS (
        SELECT 1 FROM research_decision d
        WHERE d.workspace_id = OLD.workspace_id AND d.session_id = OLD.session_id AND d.id = OLD.id
          AND d.decision_kind = 'research_method'
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'evaluation_decision' THEN
      IF EXISTS (
        SELECT 1 FROM research_decision d
        WHERE d.workspace_id = OLD.workspace_id AND d.session_id = OLD.session_id AND d.id = OLD.id
          AND d.decision_kind <> 'research_method'
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'question' THEN
      IF EXISTS (
        SELECT 1 FROM research_question q
        WHERE q.workspace_id = OLD.workspace_id AND q.session_id = OLD.session_id AND q.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'task' THEN
      IF EXISTS (
        SELECT 1 FROM research_task t
        WHERE t.workspace_id = OLD.workspace_id AND t.session_id = OLD.session_id AND t.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'attempt' THEN
      IF EXISTS (
        SELECT 1 FROM research_task_attempt a
        WHERE a.workspace_id = OLD.workspace_id AND a.session_id = OLD.session_id AND a.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'result_artifact' THEN
      IF EXISTS (
        SELECT 1 FROM research_result_artifact r
        WHERE r.workspace_id = OLD.workspace_id AND r.session_id = OLD.session_id AND r.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'legacy_source' THEN
      IF EXISTS (
        SELECT 1 FROM research_source s
        WHERE s.workspace_id = OLD.workspace_id AND s.session_id = OLD.session_id AND s.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'source_snapshot' THEN
      IF EXISTS (
        SELECT 1 FROM research_source_snapshot s
        WHERE s.workspace_id = OLD.workspace_id AND s.session_id = OLD.session_id AND s.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'observation' THEN
      IF EXISTS (
        SELECT 1 FROM research_observation o
        WHERE o.workspace_id = OLD.workspace_id AND o.session_id = OLD.session_id AND o.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'claim' THEN
      IF EXISTS (
        SELECT 1 FROM research_claim c
        WHERE c.workspace_id = OLD.workspace_id AND c.session_id = OLD.session_id AND c.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'evidence_link' THEN
      IF EXISTS (
        SELECT 1 FROM research_claim_evidence e
        WHERE e.workspace_id = OLD.workspace_id AND e.session_id = OLD.session_id AND e.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'report_revision' THEN
      IF EXISTS (
        SELECT 1 FROM research_report r
        WHERE r.workspace_id = OLD.workspace_id AND r.session_id = OLD.session_id AND r.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'stage_evaluation' THEN
      IF EXISTS (
        SELECT 1 FROM research_stage_eval e
        WHERE e.workspace_id = OLD.workspace_id AND e.session_id = OLD.session_id AND e.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'research_message' THEN
      IF EXISTS (
        SELECT 1 FROM research_message m
        WHERE m.workspace_id = OLD.workspace_id AND m.session_id = OLD.session_id AND m.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'product_round_decision' THEN
      IF EXISTS (
        SELECT 1 FROM research_product_round_card p
        WHERE p.workspace_id = OLD.workspace_id AND p.session_id = OLD.session_id AND p.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'context_manifest' THEN
      IF EXISTS (
        SELECT 1 FROM research_artifact_context_manifest m
        WHERE m.workspace_id = OLD.workspace_id AND m.session_id = OLD.session_id AND m.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'run_event' THEN
      IF EXISTS (
        SELECT 1 FROM research_run_event e
        WHERE e.workspace_id = OLD.workspace_id AND e.session_id = OLD.session_id AND e.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'graph_node' THEN
      IF EXISTS (
        SELECT 1 FROM research_graph_node n
        WHERE n.workspace_id = OLD.workspace_id AND n.session_id = OLD.session_id AND n.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    WHEN 'graph_edge' THEN
      IF EXISTS (
        SELECT 1 FROM research_graph_edge e
        WHERE e.workspace_id = OLD.workspace_id AND e.session_id = OLD.session_id AND e.id = OLD.id
      ) THEN
        RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
          USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
      END IF;
    ELSE
      RAISE EXCEPTION 'research artifact passport cannot be deleted while domain row exists'
        USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_passport_delete_guard';
  END CASE;
  RETURN OLD;
END;
$$;

CREATE TRIGGER research_artifact_passport_delete_guard
BEFORE DELETE ON research_artifact_passport
FOR EACH ROW EXECUTE FUNCTION research_artifact_passport_delete_guard_fn();

-- Domain insert guards (deferred).
CREATE CONSTRAINT TRIGGER research_session_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id ON research_session
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_session_artifact_passport_guard_fn();

CREATE CONSTRAINT TRIGGER research_contract_revision_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_contract_revision
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('contract_revision');

CREATE CONSTRAINT TRIGGER research_decision_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id, decision_kind ON research_decision
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_decision_artifact_passport_guard_fn();

CREATE CONSTRAINT TRIGGER research_question_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_question
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('question');

CREATE CONSTRAINT TRIGGER research_task_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_task
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('task');

CREATE CONSTRAINT TRIGGER research_task_attempt_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_task_attempt
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('attempt');

CREATE CONSTRAINT TRIGGER research_result_artifact_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_result_artifact
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('result_artifact');

CREATE CONSTRAINT TRIGGER research_source_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_source
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('legacy_source');

CREATE CONSTRAINT TRIGGER research_source_snapshot_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_source_snapshot
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('source_snapshot');

CREATE CONSTRAINT TRIGGER research_observation_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_observation
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('observation');

CREATE CONSTRAINT TRIGGER research_claim_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_claim
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('claim');

CREATE CONSTRAINT TRIGGER research_claim_evidence_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_claim_evidence
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('evidence_link');

CREATE CONSTRAINT TRIGGER research_report_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_report
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('report_revision');

CREATE CONSTRAINT TRIGGER research_stage_eval_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_stage_eval
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('stage_evaluation');

CREATE CONSTRAINT TRIGGER research_message_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_message
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('research_message');

CREATE CONSTRAINT TRIGGER research_product_round_card_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_product_round_card
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('product_round_decision');

CREATE CONSTRAINT TRIGGER research_artifact_context_manifest_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_artifact_context_manifest
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('context_manifest');

CREATE CONSTRAINT TRIGGER research_run_event_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_run_event
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('run_event');

CREATE CONSTRAINT TRIGGER research_graph_node_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_graph_node
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('graph_node');

CREATE CONSTRAINT TRIGGER research_graph_edge_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_graph_edge
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('graph_edge');

-- Domain delete guards (allow cascade once session row is gone).
CREATE TRIGGER research_session_artifact_passport_delete_guard
BEFORE DELETE ON research_session
FOR EACH ROW EXECUTE FUNCTION research_session_artifact_passport_delete_guard_fn();

CREATE TRIGGER research_contract_revision_artifact_passport_delete_guard
BEFORE DELETE ON research_contract_revision
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('contract_revision');

CREATE TRIGGER research_decision_artifact_passport_delete_guard
BEFORE DELETE ON research_decision
FOR EACH ROW EXECUTE FUNCTION research_decision_artifact_passport_delete_guard_fn();

CREATE TRIGGER research_question_artifact_passport_delete_guard
BEFORE DELETE ON research_question
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('question');

CREATE TRIGGER research_task_artifact_passport_delete_guard
BEFORE DELETE ON research_task
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('task');

CREATE TRIGGER research_task_attempt_artifact_passport_delete_guard
BEFORE DELETE ON research_task_attempt
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('attempt');

CREATE TRIGGER research_result_artifact_artifact_passport_delete_guard
BEFORE DELETE ON research_result_artifact
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('result_artifact');

CREATE TRIGGER research_source_artifact_passport_delete_guard
BEFORE DELETE ON research_source
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('legacy_source');

CREATE TRIGGER research_source_snapshot_artifact_passport_delete_guard
BEFORE DELETE ON research_source_snapshot
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('source_snapshot');

CREATE TRIGGER research_observation_artifact_passport_delete_guard
BEFORE DELETE ON research_observation
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('observation');

CREATE TRIGGER research_claim_artifact_passport_delete_guard
BEFORE DELETE ON research_claim
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('claim');

CREATE TRIGGER research_claim_evidence_artifact_passport_delete_guard
BEFORE DELETE ON research_claim_evidence
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('evidence_link');

CREATE TRIGGER research_report_artifact_passport_delete_guard
BEFORE DELETE ON research_report
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('report_revision');

CREATE TRIGGER research_stage_eval_artifact_passport_delete_guard
BEFORE DELETE ON research_stage_eval
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('stage_evaluation');

CREATE TRIGGER research_message_artifact_passport_delete_guard
BEFORE DELETE ON research_message
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('research_message');

CREATE TRIGGER research_product_round_card_artifact_passport_delete_guard
BEFORE DELETE ON research_product_round_card
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('product_round_decision');

CREATE TRIGGER research_artifact_context_manifest_artifact_passport_delete_guard
BEFORE DELETE ON research_artifact_context_manifest
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('context_manifest');

CREATE TRIGGER research_run_event_artifact_passport_delete_guard
BEFORE DELETE ON research_run_event
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('run_event');

CREATE TRIGGER research_graph_node_artifact_passport_delete_guard
BEFORE DELETE ON research_graph_node
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('graph_node');

CREATE TRIGGER research_graph_edge_artifact_passport_delete_guard
BEFORE DELETE ON research_graph_edge
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('graph_edge');

CREATE OR REPLACE FUNCTION research_ensure_run_session_passport(
  p_workspace_id UUID,
  p_session_id UUID
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
  INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
  VALUES (p_workspace_id, p_session_id, 'legacy-v1-v5-compat-v1', 0)
  ON CONFLICT (workspace_id, session_id) DO NOTHING;
  PERFORM research_artifact_backfill_registered(
    p_workspace_id,
    p_session_id,
    p_session_id,
    'run_session',
    COALESCE(
      (SELECT created_at FROM research_session s WHERE s.workspace_id = p_workspace_id AND s.id = p_session_id),
      now()
    ),
    NULL,
    NULL
  );
END;
$$;
