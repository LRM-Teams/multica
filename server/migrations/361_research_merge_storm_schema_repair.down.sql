DROP TRIGGER IF EXISTS research_screening_decision_artifact_passport_delete_guard ON research_screening_decision;
DROP TRIGGER IF EXISTS research_source_candidate_artifact_passport_delete_guard ON research_source_candidate;
DROP TRIGGER IF EXISTS research_query_execution_artifact_passport_delete_guard ON research_query_execution;
DROP TRIGGER IF EXISTS research_search_plan_artifact_passport_delete_guard ON research_search_plan;

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

ALTER TABLE research_task_inquiry_target
  DROP CONSTRAINT IF EXISTS research_task_inquiry_target_attempt_fk;

ALTER TABLE research_task_inquiry_target
  DROP COLUMN IF EXISTS bound_by_attempt_id,
  DROP COLUMN IF EXISTS plan_version,
  DROP COLUMN IF EXISTS goal_version;
