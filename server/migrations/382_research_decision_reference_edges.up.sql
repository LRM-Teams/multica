-- Chapter D §4.8: backfill exact-version Decision input/outcome lineage.

CREATE OR REPLACE FUNCTION research_artifact_insert_decision_reference(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID,p_owner_kind TEXT,
  p_input_id UUID,p_input_kind TEXT,p_relation TEXT,p_ordinal INTEGER,p_field_path TEXT
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_consumer UUID; v_input UUID;
BEGIN
  SELECT version.id INTO v_consumer FROM research_artifact_passport passport
  JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE passport.workspace_id=p_workspace_id AND passport.session_id=p_session_id
    AND passport.id=p_decision_id AND passport.entity_kind=p_owner_kind;
  IF v_consumer IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_decision_id,'/artifact_version',
      p_owner_kind||'_version',p_decision_id::text,'unresolved_reference'
    );
    RETURN 1;
  END IF;
  SELECT version.id INTO v_input FROM research_artifact_passport passport
  JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE passport.workspace_id=p_workspace_id AND passport.session_id=p_session_id
    AND passport.id=p_input_id AND passport.entity_kind=p_input_kind;
  IF v_input IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_decision_id,p_field_path,
      p_input_kind||'_version',p_input_id::text,'unresolved_reference'
    );
    RETURN 1;
  END IF;
  IF EXISTS (SELECT 1 FROM research_artifact_input_reference reference
    WHERE reference.workspace_id=p_workspace_id AND reference.session_id=p_session_id
      AND reference.consumer_version_id=v_consumer AND reference.input_version_id=v_input
      AND reference.relation=p_relation) THEN RETURN 0; END IF;
  INSERT INTO research_artifact_input_reference(
    workspace_id,session_id,consumer_version_id,input_version_id,relation,
    explicitly_used,purpose,ordinal
  ) VALUES(p_workspace_id,p_session_id,v_consumer,v_input,p_relation,true,
    'decision_relationship_migration',p_ordinal);
  RETURN 0;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_decision_array_references(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID,p_owner_kind TEXT,
  p_values JSONB,p_input_kind TEXT,p_relation TEXT,p_field_path TEXT
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_value JSONB; v_ordinal BIGINT; v_total INTEGER := 0;
BEGIN
  IF p_values IS NULL OR p_values='null'::jsonb THEN RETURN 0; END IF;
  FOR v_value,v_ordinal IN SELECT value,ordinality FROM jsonb_array_elements(p_values) WITH ORDINALITY LOOP
    v_total:=v_total+research_artifact_insert_decision_reference(
      p_workspace_id,p_session_id,p_decision_id,p_owner_kind,(v_value#>>'{}')::uuid,
      p_input_kind,p_relation,(v_ordinal-1)::integer,p_field_path||'/'||(v_ordinal-1)
    );
  END LOOP;
  RETURN v_total;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_decision_references_if_clean(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_kind TEXT; v_owner_kind TEXT; v_inputs JSONB; v_outcome JSONB;
  v_diagnostics INTEGER; v_total INTEGER := 0;
BEGIN
  SELECT count(*)::int INTO v_diagnostics FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND owner_id=p_decision_id
    AND owner_kind IN ('method_decision','evaluation_decision');
  IF v_diagnostics>0 THEN RETURN 0; END IF;
  SELECT decision.decision_kind,decision.inputs,decision.outcome
  INTO v_kind,v_inputs,v_outcome FROM research_decision decision
  WHERE decision.workspace_id=p_workspace_id AND decision.session_id=p_session_id AND decision.id=p_decision_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  v_owner_kind:=CASE WHEN v_kind='research_method' THEN 'method_decision' ELSE 'evaluation_decision' END;
  v_inputs:=COALESCE(v_inputs,'{}'::jsonb); v_outcome:=COALESCE(v_outcome,'{}'::jsonb);
  IF btrim(COALESCE(v_inputs->>'task_id',''))<>'' THEN v_total:=v_total+research_artifact_insert_decision_reference(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,(v_inputs->>'task_id')::uuid,'task','decision_input_task',0,'/inputs/task_id'); END IF;
  IF btrim(COALESCE(v_inputs->>'attempt_id',''))<>'' THEN v_total:=v_total+research_artifact_insert_decision_reference(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,(v_inputs->>'attempt_id')::uuid,'attempt','decision_input_attempt',0,'/inputs/attempt_id'); END IF;
  IF btrim(COALESCE(v_inputs->>'question_id',''))<>'' THEN v_total:=v_total+research_artifact_insert_decision_reference(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,(v_inputs->>'question_id')::uuid,'question','decision_input_question',0,'/inputs/question_id'); END IF;
  IF btrim(COALESCE(v_inputs->>'report_id',''))<>'' THEN v_total:=v_total+research_artifact_insert_decision_reference(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,(v_inputs->>'report_id')::uuid,'report_revision','decision_input_report',0,'/inputs/report_id'); END IF;
  IF btrim(COALESCE(v_outcome->>'created_by_task_id',''))<>'' THEN v_total:=v_total+research_artifact_insert_decision_reference(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,(v_outcome->>'created_by_task_id')::uuid,'task','decision_creator_task',0,'/outcome/created_by_task_id'); END IF;
  IF btrim(COALESCE(v_outcome->>'task_id',''))<>'' THEN v_total:=v_total+research_artifact_insert_decision_reference(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,(v_outcome->>'task_id')::uuid,'task','decision_outcome_task',0,'/outcome/task_id'); END IF;
  IF btrim(COALESCE(v_outcome->>'attempt_id',''))<>'' THEN v_total:=v_total+research_artifact_insert_decision_reference(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,(v_outcome->>'attempt_id')::uuid,'attempt','decision_outcome_attempt',0,'/outcome/attempt_id'); END IF;
  IF btrim(COALESCE(v_outcome->>'question_id',''))<>'' THEN v_total:=v_total+research_artifact_insert_decision_reference(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,(v_outcome->>'question_id')::uuid,'question','decision_outcome_question',0,'/outcome/question_id'); END IF;
  IF btrim(COALESCE(v_outcome->>'report_id',''))<>'' THEN v_total:=v_total+research_artifact_insert_decision_reference(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,(v_outcome->>'report_id')::uuid,'report_revision','decision_outcome_report',0,'/outcome/report_id'); END IF;
  IF btrim(COALESCE(v_outcome->>'evaluation_decision_id',''))<>'' THEN v_total:=v_total+research_artifact_insert_decision_reference(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,(v_outcome->>'evaluation_decision_id')::uuid,'evaluation_decision','decision_evaluation',0,'/outcome/evaluation_decision_id'); END IF;
  v_total:=v_total+research_artifact_materialize_decision_array_references(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,v_inputs->'affected_branch_ids','branch','decision_affected_branch','/inputs/affected_branch_ids');
  v_total:=v_total+research_artifact_materialize_decision_array_references(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,v_outcome->'impacted_branch_ids','branch','decision_impacted_branch','/outcome/impacted_branch_ids');
  v_total:=v_total+research_artifact_materialize_decision_array_references(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,v_outcome->'obsolete_branch_ids','branch','decision_obsolete_branch','/outcome/obsolete_branch_ids');
  v_total:=v_total+research_artifact_materialize_decision_array_references(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,v_outcome->'obsolete_task_ids','task','decision_obsolete_task','/outcome/obsolete_task_ids');
  v_total:=v_total+research_artifact_materialize_decision_array_references(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,v_outcome->'cancel_running_task_ids','task','decision_cancel_task','/outcome/cancel_running_task_ids');
  v_total:=v_total+research_artifact_materialize_decision_array_references(p_workspace_id,p_session_id,p_decision_id,v_owner_kind,v_outcome->'retained_running_task_ids','task','decision_retained_task','/outcome/retained_running_task_ids');
  RETURN v_total;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_decision_references(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_diagnostics INTEGER;
BEGIN
  v_diagnostics:=research_artifact_scan_research_decision_migration_diagnostics(p_workspace_id,p_session_id,p_decision_id);
  IF v_diagnostics>0 THEN RETURN v_diagnostics; END IF;
  RETURN research_artifact_materialize_decision_references_if_clean(p_workspace_id,p_session_id,p_decision_id);
END;
$$;

ALTER FUNCTION research_artifact_scan_session_migration_diagnostics(UUID,UUID)
  RENAME TO research_artifact_scan_session_migration_diagnostics_v381;
CREATE FUNCTION research_artifact_scan_session_migration_diagnostics(p_workspace_id UUID,p_session_id UUID)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_owner_id UUID; v_total INTEGER;
BEGIN
  v_total:=research_artifact_scan_session_migration_diagnostics_v381(p_workspace_id,p_session_id);
  FOR v_owner_id IN SELECT id FROM research_decision WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_materialize_decision_references_if_clean(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  RETURN v_total;
END;
$$;

DO $$ DECLARE v_decision RECORD; BEGIN
  FOR v_decision IN SELECT workspace_id,session_id,id FROM research_decision LOOP
    PERFORM research_artifact_materialize_decision_references(v_decision.workspace_id,v_decision.session_id,v_decision.id);
  END LOOP;
END $$;
