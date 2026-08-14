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
      AND m.mutation_kind = 'verification'
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
