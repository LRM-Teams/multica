-- Chapter D §4.8: resolve Claim evidence_standard_key against its frozen Method.

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT parser IN (
    'research_message_match_decision','research_decision_inputs',
    'research_report_structured','research_run_event_payload',
    'research_graph_node_payload','research_legacy_source_payload',
    'research_task_remediation_acceptance_criteria',
    'research_decision_evaluation_local_references',
    'research_claim_method_evidence_standard'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_claim_method_diagnostics(
  p_workspace_id UUID,p_session_id UUID,p_claim_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_key TEXT; v_goal_version INTEGER; v_plan_version INTEGER;
  v_method JSONB; v_matches BIGINT := 0; v_count INTEGER;
BEGIN
  DELETE FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='claim' AND owner_id=p_claim_id
    AND field_path='/evidence_standard_key';
  SELECT evidence_standard_key,goal_version,plan_version
    INTO v_key,v_goal_version,v_plan_version
  FROM research_claim
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_claim_id;
  IF NOT FOUND OR btrim(COALESCE(v_key,''))='' THEN RETURN 0; END IF;

  SELECT outcome INTO v_method FROM research_decision
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND decision_kind='research_method' AND goal_version=v_goal_version
    AND plan_version=v_plan_version
  ORDER BY created_at DESC,id DESC LIMIT 1;
  IF v_method IS NULL OR jsonb_typeof(v_method->'evidence_standards')<>'array' THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'claim',p_claim_id,'/evidence_standard_key',
      'method_evidence_standard',v_key,
      CASE WHEN v_method IS NULL THEN 'dangling_local_key' ELSE 'unknown_schema' END
    );
  ELSE
    SELECT count(*) INTO v_matches
    FROM jsonb_array_elements(v_method->'evidence_standards') standard
    WHERE standard->>'client_key'=v_key;
    IF v_matches=0 THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id,p_session_id,'claim',p_claim_id,'/evidence_standard_key',
        'method_evidence_standard',v_key,'dangling_local_key'
      );
    ELSIF v_matches>1 THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id,p_session_id,'claim',p_claim_id,'/evidence_standard_key',
        'method_evidence_standard',v_key,'ambiguous_local_key'
      );
    END IF;
  END IF;
  SELECT count(*)::int INTO v_count FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='claim' AND owner_id=p_claim_id AND field_path='/evidence_standard_key';
  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_claim_method_diagnostic_refresh_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM research_artifact_scan_research_claim_method_diagnostics(NEW.workspace_id,NEW.session_id,NEW.id);
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_claim_method_standard_diagnostic_refresh
AFTER INSERT OR UPDATE OF workspace_id,session_id,goal_version,plan_version,evidence_standard_key
ON research_claim FOR EACH ROW
EXECUTE FUNCTION research_artifact_claim_method_diagnostic_refresh_fn();

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
    IF to_regprocedure('research_artifact_scan_research_evaluation_local_diagnostics(uuid,uuid,uuid)') IS NOT NULL THEN
      EXECUTE 'SELECT research_artifact_scan_research_evaluation_local_diagnostics($1,$2,$3)' INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total:=v_total+v_scanned;
    END IF;
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_report WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_report_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_run_event WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_run_event_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  IF to_regprocedure('research_artifact_scan_research_claim_method_diagnostics(uuid,uuid,uuid)') IS NOT NULL THEN
    FOR v_owner_id IN SELECT id FROM research_claim WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
      EXECUTE 'SELECT research_artifact_scan_research_claim_method_diagnostics($1,$2,$3)' INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total:=v_total+v_scanned;
    END LOOP;
  END IF;
  IF to_regprocedure('research_artifact_scan_research_task_migration_diagnostics(uuid,uuid,uuid)') IS NOT NULL THEN
    FOR v_owner_id IN SELECT id FROM research_task WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
      EXECUTE 'SELECT research_artifact_scan_research_task_migration_diagnostics($1,$2,$3)' INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total:=v_total+v_scanned;
    END LOOP;
  END IF;
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

DO $$ DECLARE v_claim RECORD; BEGIN
  FOR v_claim IN SELECT workspace_id,session_id,id FROM research_claim LOOP
    PERFORM research_artifact_scan_research_claim_method_diagnostics(v_claim.workspace_id,v_claim.session_id,v_claim.id);
  END LOOP;
END $$;
