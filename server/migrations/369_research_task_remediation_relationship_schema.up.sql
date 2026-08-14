-- Chapter D §4.8: validate typed references emitted into system remediation Tasks.

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT parser IN (
    'research_message_match_decision','research_decision_inputs',
    'research_report_structured','research_run_event_payload',
    'research_graph_node_payload','research_legacy_source_payload',
    'research_task_remediation_acceptance_criteria'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_task_scoped_uuid_reference(
  p_workspace_id UUID,p_session_id UUID,p_task_id UUID,p_field_path TEXT,
  p_target_kind TEXT,p_reference_value TEXT
)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE v_same_scope BOOLEAN; v_any_scope BOOLEAN;
BEGIN
  IF btrim(COALESCE(p_reference_value,''))='' THEN RETURN; END IF;
  IF NOT research_artifact_reference_uuid_valid(p_reference_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'task',p_task_id,p_field_path,
      p_target_kind,p_reference_value,'malformed_uuid'
    );
    RETURN;
  END IF;
  CASE p_target_kind
    WHEN 'question' THEN
      SELECT EXISTS(SELECT 1 FROM research_question WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_reference_value::uuid),
             EXISTS(SELECT 1 FROM research_question WHERE id=p_reference_value::uuid)
        INTO v_same_scope,v_any_scope;
    WHEN 'claim' THEN
      SELECT EXISTS(SELECT 1 FROM research_claim WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_reference_value::uuid),
             EXISTS(SELECT 1 FROM research_claim WHERE id=p_reference_value::uuid)
        INTO v_same_scope,v_any_scope;
    WHEN 'evaluation_decision' THEN
      SELECT EXISTS(SELECT 1 FROM research_decision WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_reference_value::uuid),
             EXISTS(SELECT 1 FROM research_decision WHERE id=p_reference_value::uuid)
        INTO v_same_scope,v_any_scope;
    WHEN 'report_revision' THEN
      SELECT EXISTS(SELECT 1 FROM research_report WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_reference_value::uuid),
             EXISTS(SELECT 1 FROM research_report WHERE id=p_reference_value::uuid)
        INTO v_same_scope,v_any_scope;
    WHEN 'task' THEN
      SELECT EXISTS(SELECT 1 FROM research_task WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_reference_value::uuid),
             EXISTS(SELECT 1 FROM research_task WHERE id=p_reference_value::uuid)
        INTO v_same_scope,v_any_scope;
    WHEN 'attempt' THEN
      SELECT EXISTS(SELECT 1 FROM research_task_attempt WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_reference_value::uuid),
             EXISTS(SELECT 1 FROM research_task_attempt WHERE id=p_reference_value::uuid)
        INTO v_same_scope,v_any_scope;
    ELSE RAISE EXCEPTION 'unknown remediation UUID target kind %',p_target_kind;
  END CASE;
  IF NOT v_same_scope THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'task',p_task_id,p_field_path,
      p_target_kind,p_reference_value,
      CASE WHEN v_any_scope THEN 'cross_scope_reference' ELSE 'unresolved_reference' END
    );
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_task_local_reference(
  p_workspace_id UUID,p_session_id UUID,p_task_id UUID,p_goal_version INTEGER,
  p_plan_version INTEGER,p_field_path TEXT,p_target_kind TEXT,p_reference_value TEXT
)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE v_matches BIGINT := 0; v_method JSONB;
BEGIN
  IF btrim(COALESCE(p_reference_value,''))='' THEN RETURN; END IF;
  CASE p_target_kind
    WHEN 'claim' THEN
      SELECT count(*) INTO v_matches FROM research_claim
      WHERE workspace_id=p_workspace_id AND session_id=p_session_id
        AND goal_version=p_goal_version AND plan_version=p_plan_version
        AND client_key=p_reference_value;
    WHEN 'evidence_standard' THEN
      SELECT outcome INTO v_method FROM research_decision
      WHERE workspace_id=p_workspace_id AND session_id=p_session_id
        AND decision_kind='research_method' AND goal_version=p_goal_version
        AND plan_version=p_plan_version
      ORDER BY created_at DESC,id DESC LIMIT 1;
      IF v_method IS NOT NULL AND jsonb_typeof(v_method->'evidence_standards')='array' THEN
        SELECT count(*) INTO v_matches
        FROM jsonb_array_elements(v_method->'evidence_standards') standard
        WHERE standard->>'client_key'=p_reference_value;
      END IF;
    ELSE RAISE EXCEPTION 'unknown remediation local target kind %',p_target_kind;
  END CASE;
  IF v_matches=0 THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'task',p_task_id,p_field_path,
      p_target_kind,p_reference_value,'dangling_local_key'
    );
  ELSIF v_matches>1 THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'task',p_task_id,p_field_path,
      p_target_kind,p_reference_value,'ambiguous_local_key'
    );
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_task_migration_diagnostics(
  p_workspace_id UUID,p_session_id UUID,p_task_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_client_key TEXT; v_goal_version INTEGER; v_plan_version INTEGER;
  v_criteria JSONB; v_targets JSONB; v_finding JSONB; v_metadata JSONB;
  v_ordinal BIGINT; v_base TEXT; v_count INTEGER;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'task',p_task_id
  );
  SELECT client_key,goal_version,plan_version,acceptance_criteria
    INTO v_client_key,v_goal_version,v_plan_version,v_criteria
  FROM research_task WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_task_id;
  IF NOT FOUND OR v_client_key NOT LIKE 'control:%' THEN RETURN 0; END IF;
  IF jsonb_typeof(v_criteria)<>'object' OR jsonb_typeof(v_criteria->'remediation')<>'object' THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'task',p_task_id,'/acceptance_criteria/remediation',
      'remediation_schema',COALESCE(jsonb_typeof(v_criteria->'remediation'),'null'),'unknown_schema'
    );
  ELSE
    v_targets := v_criteria#>'{remediation,target_findings}';
    IF jsonb_typeof(v_targets)<>'array' THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id,p_session_id,'task',p_task_id,
        '/acceptance_criteria/remediation/target_findings','gate_finding_array',
        COALESCE(jsonb_typeof(v_targets),'null'),'unknown_schema'
      );
    ELSE
      FOR v_finding,v_ordinal IN SELECT value,ordinality FROM jsonb_array_elements(v_targets) WITH ORDINALITY LOOP
        v_base := '/acceptance_criteria/remediation/target_findings/'||(v_ordinal-1)||'/metadata';
        v_metadata := v_finding->'metadata';
        IF v_metadata IS NULL THEN CONTINUE; END IF;
        IF jsonb_typeof(v_metadata)<>'object' THEN
          PERFORM research_artifact_record_migration_diagnostic(
            p_workspace_id,p_session_id,'task',p_task_id,v_base,'gate_finding_metadata',
            jsonb_typeof(v_metadata),'unknown_schema'
          );
          CONTINUE;
        END IF;
        PERFORM research_artifact_diagnose_task_scoped_uuid_reference(p_workspace_id,p_session_id,p_task_id,v_base||'/question_id','question',v_metadata->>'question_id');
        PERFORM research_artifact_diagnose_task_scoped_uuid_reference(p_workspace_id,p_session_id,p_task_id,v_base||'/answer_claim_id','claim',v_metadata->>'answer_claim_id');
        PERFORM research_artifact_diagnose_task_scoped_uuid_reference(p_workspace_id,p_session_id,p_task_id,v_base||'/evaluation_decision_id','evaluation_decision',v_metadata->>'evaluation_decision_id');
        PERFORM research_artifact_diagnose_task_scoped_uuid_reference(p_workspace_id,p_session_id,p_task_id,v_base||'/report_id','report_revision',v_metadata->>'report_id');
        PERFORM research_artifact_diagnose_task_scoped_uuid_reference(p_workspace_id,p_session_id,p_task_id,v_base||'/task_id','task',v_metadata->>'task_id');
        PERFORM research_artifact_diagnose_task_scoped_uuid_reference(p_workspace_id,p_session_id,p_task_id,v_base||'/attempt_id','attempt',v_metadata->>'attempt_id');
        PERFORM research_artifact_diagnose_task_local_reference(p_workspace_id,p_session_id,p_task_id,v_goal_version,v_plan_version,v_base||'/claim_key','claim',v_metadata->>'claim_key');
        PERFORM research_artifact_diagnose_task_local_reference(p_workspace_id,p_session_id,p_task_id,v_goal_version,v_plan_version,v_base||'/evidence_standard_key','evidence_standard',v_metadata->>'evidence_standard_key');
      END LOOP;
    END IF;
  END IF;
  SELECT count(*)::int INTO v_count FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='task' AND owner_id=p_task_id;
  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_task_remediation_diagnostic_refresh_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM research_artifact_scan_research_task_migration_diagnostics(NEW.workspace_id,NEW.session_id,NEW.id);
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_task_remediation_relationship_diagnostic_refresh
AFTER INSERT OR UPDATE OF workspace_id,session_id,client_key,goal_version,plan_version,acceptance_criteria
ON research_task FOR EACH ROW
EXECUTE FUNCTION research_artifact_task_remediation_diagnostic_refresh_fn();

CREATE OR REPLACE FUNCTION research_artifact_scan_session_migration_diagnostics(
  p_workspace_id UUID,p_session_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_owner_id UUID; v_total INTEGER := 0; v_scanned INTEGER;
BEGIN
  FOR v_owner_id IN SELECT id FROM research_message WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_message_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_decision WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_decision_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_report WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_report_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_run_event WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_run_event_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_task WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_task_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  IF to_regprocedure('research_artifact_scan_research_graph_node_migration_diagnostics(uuid,uuid,uuid)') IS NOT NULL THEN
    FOR v_owner_id IN SELECT id FROM research_graph_node WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
      EXECUTE 'SELECT research_artifact_scan_research_graph_node_migration_diagnostics($1,$2,$3)' INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total:=v_total+v_scanned;
    END LOOP;
  END IF;
  IF to_regprocedure('research_artifact_scan_research_legacy_source_migration_diagnostics(uuid,uuid,uuid)') IS NOT NULL THEN
    FOR v_owner_id IN SELECT id FROM research_source WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
      EXECUTE 'SELECT research_artifact_scan_research_legacy_source_migration_diagnostics($1,$2,$3)' INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total:=v_total+v_scanned;
    END LOOP;
  END IF;
  RETURN v_total;
END;
$$;

DO $$ DECLARE v_task RECORD; BEGIN
  FOR v_task IN SELECT workspace_id,session_id,id FROM research_task WHERE client_key LIKE 'control:%' LOOP
    PERFORM research_artifact_scan_research_task_migration_diagnostics(v_task.workspace_id,v_task.session_id,v_task.id);
  END LOOP;
END $$;
