-- Chapter D §4.8: close Decision schemas and scan both inputs and outcomes.

CREATE OR REPLACE FUNCTION research_artifact_decision_relationship_schema_allowed(decision_kind TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT decision_kind IN (
    'budget_exhausted','citation_audit','information_gain',
    'quality_gate','remediation_routing','research_method','selective_steering'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_scoped_question_reference(
  p_workspace_id UUID,p_session_id UUID,p_owner_kind TEXT,p_owner_id UUID,
  p_field_path TEXT,p_reference_value TEXT
)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
  IF btrim(COALESCE(p_reference_value,''))='' THEN RETURN; END IF;
  IF NOT research_artifact_reference_uuid_valid(p_reference_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
      'question',p_reference_value,'malformed_uuid'
    );
    RETURN;
  END IF;
  IF NOT EXISTS(SELECT 1 FROM research_question question
                WHERE question.workspace_id=p_workspace_id AND question.session_id=p_session_id
                  AND question.id=p_reference_value::uuid) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
      'question',p_reference_value,
      CASE WHEN EXISTS(SELECT 1 FROM research_question question
                       WHERE question.id=p_reference_value::uuid
                         AND (question.workspace_id IS DISTINCT FROM p_workspace_id
                           OR question.session_id IS DISTINCT FROM p_session_id))
        THEN 'cross_scope_reference' ELSE 'unresolved_reference' END
    );
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_scoped_decision_reference(
  p_workspace_id UUID,p_session_id UUID,p_owner_kind TEXT,p_owner_id UUID,
  p_field_path TEXT,p_reference_value TEXT
)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
  IF btrim(COALESCE(p_reference_value,''))='' THEN RETURN; END IF;
  IF NOT research_artifact_reference_uuid_valid(p_reference_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
      'evaluation_decision',p_reference_value,'malformed_uuid'
    );
    RETURN;
  END IF;
  IF NOT EXISTS(SELECT 1 FROM research_decision decision
                WHERE decision.workspace_id=p_workspace_id AND decision.session_id=p_session_id
                  AND decision.id=p_reference_value::uuid) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
      'evaluation_decision',p_reference_value,
      CASE WHEN EXISTS(SELECT 1 FROM research_decision decision
                       WHERE decision.id=p_reference_value::uuid
                         AND (decision.workspace_id IS DISTINCT FROM p_workspace_id
                           OR decision.session_id IS DISTINCT FROM p_session_id))
        THEN 'cross_scope_reference' ELSE 'unresolved_reference' END
    );
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_scoped_branch_reference(
  p_workspace_id UUID,p_session_id UUID,p_owner_kind TEXT,p_owner_id UUID,
  p_field_path TEXT,p_reference_value TEXT
)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
  IF btrim(COALESCE(p_reference_value,''))='' THEN RETURN; END IF;
  IF NOT research_artifact_reference_uuid_valid(p_reference_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
      'branch',p_reference_value,'malformed_uuid'
    );
    RETURN;
  END IF;
  IF NOT EXISTS(SELECT 1 FROM research_branch branch
                WHERE branch.workspace_id=p_workspace_id AND branch.session_id=p_session_id
                  AND branch.id=p_reference_value::uuid) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
      'branch',p_reference_value,
      CASE WHEN EXISTS(SELECT 1 FROM research_branch branch
                       WHERE branch.id=p_reference_value::uuid
                         AND (branch.workspace_id IS DISTINCT FROM p_workspace_id
                           OR branch.session_id IS DISTINCT FROM p_session_id))
        THEN 'cross_scope_reference' ELSE 'unresolved_reference' END
    );
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_decision_reference_array(
  p_workspace_id UUID,p_session_id UUID,p_owner_kind TEXT,p_owner_id UUID,
  p_field_path TEXT,p_expected_target_kind TEXT,p_values JSONB
)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE v_item JSONB; v_ordinal BIGINT;
BEGIN
  IF p_values IS NULL THEN RETURN; END IF;
  IF jsonb_typeof(p_values)<>'array' THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
      p_expected_target_kind,jsonb_typeof(p_values),'unknown_schema'
    );
    RETURN;
  END IF;
  FOR v_item,v_ordinal IN
    SELECT value,ordinality FROM jsonb_array_elements(p_values) WITH ORDINALITY
  LOOP
    CASE p_expected_target_kind
      WHEN 'branch' THEN
        PERFORM research_artifact_diagnose_scoped_branch_reference(
          p_workspace_id,p_session_id,p_owner_kind,p_owner_id,
          p_field_path||'/'||(v_ordinal-1),v_item#>>'{}'
        );
      WHEN 'task' THEN
        PERFORM research_artifact_diagnose_scoped_task_reference(
          p_workspace_id,p_session_id,p_owner_kind,p_owner_id,
          p_field_path||'/'||(v_ordinal-1),v_item#>>'{}'
        );
      ELSE
        RAISE EXCEPTION 'unknown Decision array target kind %',p_expected_target_kind;
    END CASE;
  END LOOP;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_decision_migration_diagnostics(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_kind TEXT;
  v_owner_kind TEXT;
  v_inputs JSONB;
  v_outcome JSONB;
  v_count INTEGER;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'evaluation_decision',p_decision_id
  );
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'method_decision',p_decision_id
  );
  SELECT decision.decision_kind,decision.inputs,decision.outcome
  INTO v_kind,v_inputs,v_outcome
  FROM research_decision decision
  WHERE decision.workspace_id=p_workspace_id AND decision.session_id=p_session_id
    AND decision.id=p_decision_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  v_owner_kind := CASE WHEN v_kind='research_method' THEN 'method_decision' ELSE 'evaluation_decision' END;
  IF NOT research_artifact_decision_relationship_schema_allowed(v_kind) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,v_owner_kind,p_decision_id,'/decision_kind',
      'decision_schema',v_kind,'unknown_schema'
    );
  END IF;
  v_inputs := COALESCE(v_inputs,'{}'::jsonb);
  v_outcome := COALESCE(v_outcome,'{}'::jsonb);

  PERFORM research_artifact_diagnose_scoped_task_reference(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,'/inputs/task_id',v_inputs->>'task_id'
  );
  PERFORM research_artifact_diagnose_scoped_attempt_reference(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,'/inputs/attempt_id',v_inputs->>'attempt_id'
  );
  PERFORM research_artifact_diagnose_scoped_question_reference(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,'/inputs/question_id',v_inputs->>'question_id'
  );
  PERFORM research_artifact_diagnose_scoped_report_reference(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,'/inputs/report_id',v_inputs->>'report_id'
  );
  PERFORM research_artifact_diagnose_scoped_task_reference(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,'/outcome/created_by_task_id',v_outcome->>'created_by_task_id'
  );
  PERFORM research_artifact_diagnose_scoped_task_reference(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,'/outcome/task_id',v_outcome->>'task_id'
  );
  PERFORM research_artifact_diagnose_scoped_attempt_reference(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,'/outcome/attempt_id',v_outcome->>'attempt_id'
  );
  PERFORM research_artifact_diagnose_scoped_question_reference(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,'/outcome/question_id',v_outcome->>'question_id'
  );
  PERFORM research_artifact_diagnose_scoped_report_reference(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,'/outcome/report_id',v_outcome->>'report_id'
  );
  PERFORM research_artifact_diagnose_scoped_decision_reference(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,
    '/outcome/evaluation_decision_id',v_outcome->>'evaluation_decision_id'
  );
  PERFORM research_artifact_diagnose_decision_reference_array(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,
    '/inputs/affected_branch_ids','branch',v_inputs->'affected_branch_ids'
  );
  PERFORM research_artifact_diagnose_decision_reference_array(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,
    '/outcome/impacted_branch_ids','branch',v_outcome->'impacted_branch_ids'
  );
  PERFORM research_artifact_diagnose_decision_reference_array(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,
    '/outcome/obsolete_branch_ids','branch',v_outcome->'obsolete_branch_ids'
  );
  PERFORM research_artifact_diagnose_decision_reference_array(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,
    '/outcome/obsolete_task_ids','task',v_outcome->'obsolete_task_ids'
  );
  PERFORM research_artifact_diagnose_decision_reference_array(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,
    '/outcome/cancel_running_task_ids','task',v_outcome->'cancel_running_task_ids'
  );
  PERFORM research_artifact_diagnose_decision_reference_array(
    p_workspace_id,p_session_id,v_owner_kind,p_decision_id,
    '/outcome/retained_running_task_ids','task',v_outcome->'retained_running_task_ids'
  );

  SELECT count(*)::int INTO v_count
  FROM research_artifact_migration_diagnostic diagnostic
  WHERE diagnostic.workspace_id=p_workspace_id AND diagnostic.session_id=p_session_id
    AND diagnostic.owner_id=p_decision_id
    AND diagnostic.owner_kind IN ('method_decision','evaluation_decision');
  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_decision_diagnostic_refresh_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM research_artifact_scan_research_decision_migration_diagnostics(
    NEW.workspace_id,NEW.session_id,NEW.id
  );
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_decision_relationship_diagnostic_refresh
AFTER INSERT OR UPDATE OF workspace_id,session_id,decision_kind,inputs,outcome
ON research_decision
FOR EACH ROW EXECUTE FUNCTION research_artifact_decision_diagnostic_refresh_fn();

DO $$
DECLARE v_decision RECORD;
BEGIN
  FOR v_decision IN SELECT workspace_id,session_id,id FROM research_decision LOOP
    PERFORM research_artifact_scan_research_decision_migration_diagnostics(
      v_decision.workspace_id,v_decision.session_id,v_decision.id
    );
  END LOOP;
END;
$$;
