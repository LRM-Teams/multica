-- Merge-storm repair:
-- 1. 352 created research_task_inquiry_target with ordinal identity; 355 then
--    added versioned columns via CREATE TABLE IF NOT EXISTS, so those columns
--    never landed. Promote the live 352 table to the 355 contract.
-- 2. Domain passport guards must report TG_NAME so per-table constraint tests
--    and operators can see which family failed.
-- 3. 353 added search/corpus passports but only a combined passport-side
--    delete guard. Reciprocal domain-row delete guards match Chapter D.

ALTER TABLE research_task_inquiry_target
  ADD COLUMN IF NOT EXISTS goal_version INTEGER,
  ADD COLUMN IF NOT EXISTS plan_version INTEGER,
  ADD COLUMN IF NOT EXISTS bound_by_attempt_id UUID;

UPDATE research_task_inquiry_target target
SET goal_version = task.goal_version,
    plan_version = task.plan_version
FROM research_task task
WHERE (task.workspace_id, task.session_id, task.id)
    = (target.workspace_id, target.session_id, target.task_id)
  AND (target.goal_version IS NULL OR target.plan_version IS NULL);

ALTER TABLE research_task_inquiry_target
  ALTER COLUMN goal_version SET NOT NULL,
  ALTER COLUMN plan_version SET NOT NULL;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'research_task_inquiry_target_attempt_fk'
      AND conrelid = 'research_task_inquiry_target'::regclass
  ) THEN
    ALTER TABLE research_task_inquiry_target
      ADD CONSTRAINT research_task_inquiry_target_attempt_fk
      FOREIGN KEY (workspace_id, session_id, bound_by_attempt_id)
      REFERENCES research_task_attempt(workspace_id, session_id, id)
      ON DELETE CASCADE;
  END IF;
END $$;

CREATE OR REPLACE FUNCTION research_artifact_domain_passport_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_artifact_entity_kind_allowed(TG_ARGV[0]) THEN
    RAISE foreign_key_violation USING CONSTRAINT = TG_NAME;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_passport p
    WHERE p.workspace_id = NEW.workspace_id
      AND p.session_id = NEW.session_id
      AND p.id = NEW.id
      AND p.entity_kind = TG_ARGV[0]
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = TG_NAME;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION research_search_lineage_entity_exists(
  p_workspace_id UUID, p_session_id UUID, p_kind TEXT, p_id UUID
) RETURNS BOOLEAN LANGUAGE plpgsql STABLE AS $$
BEGIN
  CASE p_kind
    WHEN 'search_plan' THEN
      RETURN EXISTS (
        SELECT 1 FROM research_search_plan entity
        WHERE (entity.workspace_id, entity.session_id, entity.id) = (p_workspace_id, p_session_id, p_id)
      );
    WHEN 'query_execution' THEN
      RETURN EXISTS (
        SELECT 1 FROM research_query_execution entity
        WHERE (entity.workspace_id, entity.session_id, entity.id) = (p_workspace_id, p_session_id, p_id)
      );
    WHEN 'source_candidate' THEN
      RETURN EXISTS (
        SELECT 1 FROM research_source_candidate entity
        WHERE (entity.workspace_id, entity.session_id, entity.id) = (p_workspace_id, p_session_id, p_id)
      );
    WHEN 'screening_decision' THEN
      RETURN EXISTS (
        SELECT 1 FROM research_screening_decision entity
        WHERE (entity.workspace_id, entity.session_id, entity.id) = (p_workspace_id, p_session_id, p_id)
      );
    ELSE
      RETURN false;
  END CASE;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_passport_class_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.entity_kind IN ('search_plan','query_execution','source_candidate','screening_decision') THEN
    IF NOT research_search_lineage_entity_exists(NEW.workspace_id, NEW.session_id, NEW.entity_kind, NEW.id) THEN
      RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard';
    END IF;
    RETURN NEW;
  END IF;
  CASE NEW.entity_kind
    WHEN 'run_session' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_session s
        WHERE s.workspace_id = NEW.workspace_id AND s.id = NEW.id AND s.id = NEW.session_id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'contract_revision' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_contract_revision r
        WHERE r.workspace_id = NEW.workspace_id AND r.session_id = NEW.session_id AND r.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'method_decision' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_decision d
        WHERE d.workspace_id = NEW.workspace_id AND d.session_id = NEW.session_id AND d.id = NEW.id
          AND d.decision_kind = 'research_method'
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'evaluation_decision' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_decision d
        WHERE d.workspace_id = NEW.workspace_id AND d.session_id = NEW.session_id AND d.id = NEW.id
          AND d.decision_kind <> 'research_method'
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'question' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_question q
        WHERE q.workspace_id = NEW.workspace_id AND q.session_id = NEW.session_id AND q.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'task' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_task t
        WHERE t.workspace_id = NEW.workspace_id AND t.session_id = NEW.session_id AND t.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'attempt' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_task_attempt a
        WHERE a.workspace_id = NEW.workspace_id AND a.session_id = NEW.session_id AND a.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'result_artifact' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_result_artifact r
        WHERE r.workspace_id = NEW.workspace_id AND r.session_id = NEW.session_id AND r.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'legacy_source' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_source s
        WHERE s.workspace_id = NEW.workspace_id AND s.session_id = NEW.session_id AND s.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'source_snapshot' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_source_snapshot s
        WHERE s.workspace_id = NEW.workspace_id AND s.session_id = NEW.session_id AND s.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'observation' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_observation o
        WHERE o.workspace_id = NEW.workspace_id AND o.session_id = NEW.session_id AND o.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'claim' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_claim c
        WHERE c.workspace_id = NEW.workspace_id AND c.session_id = NEW.session_id AND c.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'evidence_link' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_claim_evidence e
        WHERE e.workspace_id = NEW.workspace_id AND e.session_id = NEW.session_id AND e.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'report_revision' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_report r
        WHERE r.workspace_id = NEW.workspace_id AND r.session_id = NEW.session_id AND r.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'stage_evaluation' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_stage_eval e
        WHERE e.workspace_id = NEW.workspace_id AND e.session_id = NEW.session_id AND e.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'research_message' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_message m
        WHERE m.workspace_id = NEW.workspace_id AND m.session_id = NEW.session_id AND m.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'product_round_decision' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_product_round_card p
        WHERE p.workspace_id = NEW.workspace_id AND p.session_id = NEW.session_id AND p.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'context_manifest' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_artifact_context_manifest m
        WHERE m.workspace_id = NEW.workspace_id AND m.session_id = NEW.session_id AND m.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'run_event' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_run_event e
        WHERE e.workspace_id = NEW.workspace_id AND e.session_id = NEW.session_id AND e.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'graph_node' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_graph_node n
        WHERE n.workspace_id = NEW.workspace_id AND n.session_id = NEW.session_id AND n.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'graph_edge' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_graph_edge e
        WHERE e.workspace_id = NEW.workspace_id AND e.session_id = NEW.session_id AND e.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    ELSE
      RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard';
  END CASE;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_schema_family_allowed(
  p_schema_name TEXT,
  p_schema_version TEXT,
  p_canonicalization_version TEXT
)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT research_artifact_entity_kind_allowed(p_schema_name)
    AND p_schema_version IN ('legacy-v1', 'research-run-v6')
    AND research_artifact_canonicalization_version_allowed(p_canonicalization_version);
$$;

CREATE OR REPLACE FUNCTION research_inquiry_status_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT research_inquiry_transition_allowed(TG_ARGV[0], OLD.status, NEW.status) THEN
    RAISE check_violation USING CONSTRAINT = 'research_inquiry_status_transition_guard';
  END IF;
  IF TG_ARGV[0] = 'branch'
     AND NEW.status = 'terminated'
     AND btrim(COALESCE(to_jsonb(NEW)->>'termination_reason', '')) = '' THEN
    RAISE check_violation USING CONSTRAINT = 'research_branch_termination_reason_guard';
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_require_verification_policy_coupling(
  p_kind TEXT,
  p_workspace_id UUID,
  p_session_id UUID,
  p_entity_id UUID,
  p_old_status TEXT,
  p_new_status TEXT,
  p_constraint_name TEXT
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  v_revision BIGINT;
  v_watermark BIGINT;
BEGIN
  IF p_old_status IS NOT DISTINCT FROM p_new_status THEN
    RETURN;
  END IF;

  SELECT p.eligibility_revision, ps.watermark
  INTO v_revision, v_watermark
  FROM research_artifact_passport p
  JOIN research_artifact_policy_state ps
    ON ps.workspace_id = p.workspace_id AND ps.session_id = p.session_id
  WHERE p.workspace_id = p_workspace_id
    AND p.session_id = p_session_id
    AND p.id = p_entity_id
    AND p.entity_kind = p_kind;

  IF NOT FOUND THEN
    RAISE foreign_key_violation USING CONSTRAINT = p_constraint_name;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_policy_mutation m
    WHERE m.workspace_id = p_workspace_id
      AND m.session_id = p_session_id
      AND m.artifact_id = p_entity_id
      AND m.mutation_kind IN ('verification', 'current_version')
      AND m.old_eligibility_revision = v_revision - 1
      AND m.new_eligibility_revision = v_revision
      AND m.watermark = v_watermark
      AND m.xmin = pg_current_xact_id()::xid
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = p_constraint_name;
  END IF;
END;
$$;

DROP TRIGGER IF EXISTS research_search_plan_artifact_passport_delete_guard ON research_search_plan;
CREATE TRIGGER research_search_plan_artifact_passport_delete_guard
BEFORE DELETE ON research_search_plan
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('search_plan');

DROP TRIGGER IF EXISTS research_query_execution_artifact_passport_delete_guard ON research_query_execution;
CREATE TRIGGER research_query_execution_artifact_passport_delete_guard
BEFORE DELETE ON research_query_execution
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('query_execution');

DROP TRIGGER IF EXISTS research_source_candidate_artifact_passport_delete_guard ON research_source_candidate;
CREATE TRIGGER research_source_candidate_artifact_passport_delete_guard
BEFORE DELETE ON research_source_candidate
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('source_candidate');

DROP TRIGGER IF EXISTS research_screening_decision_artifact_passport_delete_guard ON research_screening_decision;
CREATE TRIGGER research_screening_decision_artifact_passport_delete_guard
BEFORE DELETE ON research_screening_decision
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_delete_guard_fn('screening_decision');
