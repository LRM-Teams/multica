-- Chapter D §4.8: resolve report-local references in quality/citation Decisions.

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT parser IN (
    'research_message_match_decision','research_decision_inputs',
    'research_report_structured','research_run_event_payload',
    'research_graph_node_payload','research_legacy_source_payload',
    'research_task_remediation_acceptance_criteria',
    'research_decision_evaluation_local_references'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_evaluation_local_array(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID,p_report_id UUID,
  p_field_path TEXT,p_target_kind TEXT,p_values JSONB
)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE v_item JSONB; v_ordinal BIGINT; v_value TEXT; v_matches BIGINT;
BEGIN
  IF p_values IS NULL THEN RETURN; END IF;
  IF jsonb_typeof(p_values)<>'array' THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'evaluation_decision',p_decision_id,p_field_path,
      p_target_kind,jsonb_typeof(p_values),'unknown_schema'
    );
    RETURN;
  END IF;
  FOR v_item,v_ordinal IN SELECT value,ordinality FROM jsonb_array_elements(p_values) WITH ORDINALITY LOOP
    IF jsonb_typeof(v_item)<>'string' THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id,p_session_id,'evaluation_decision',p_decision_id,
        p_field_path||'/'||(v_ordinal-1),p_target_kind,jsonb_typeof(v_item),'unknown_schema'
      );
      CONTINUE;
    END IF;
    v_value:=v_item#>>'{}';
    CASE p_target_kind
      WHEN 'report_claim' THEN
        SELECT count(DISTINCT claim.id) INTO v_matches
        FROM research_report_claim link
        JOIN research_claim claim
          ON claim.workspace_id=link.workspace_id AND claim.session_id=link.session_id
         AND claim.id=link.claim_id
        WHERE link.workspace_id=p_workspace_id AND link.session_id=p_session_id
          AND link.report_id=p_report_id AND claim.client_key=v_value;
      WHEN 'report_section' THEN
        SELECT count(*) INTO v_matches
        FROM research_report report
        CROSS JOIN LATERAL jsonb_array_elements(
          CASE WHEN jsonb_typeof(report.structured->'sections')='array'
            THEN report.structured->'sections' ELSE '[]'::jsonb END
        ) section
        WHERE report.workspace_id=p_workspace_id AND report.session_id=p_session_id
          AND report.id=p_report_id AND section->>'id'=v_value;
      ELSE RAISE EXCEPTION 'unknown evaluation local target kind %',p_target_kind;
    END CASE;
    IF btrim(v_value)='' OR v_matches=0 THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id,p_session_id,'evaluation_decision',p_decision_id,
        p_field_path||'/'||(v_ordinal-1),p_target_kind,v_value,'dangling_local_key'
      );
    ELSIF v_matches>1 THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id,p_session_id,'evaluation_decision',p_decision_id,
        p_field_path||'/'||(v_ordinal-1),p_target_kind,v_value,'ambiguous_local_key'
      );
    END IF;
  END LOOP;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_evaluation_local_diagnostics(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_kind TEXT; v_inputs JSONB; v_outcome JSONB; v_report_text TEXT; v_report_id UUID;
  v_defect JSONB; v_ordinal BIGINT; v_base TEXT; v_count INTEGER;
BEGIN
  DELETE FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='evaluation_decision' AND owner_id=p_decision_id
    AND (field_path LIKE '/outcome/reviewed\_%' ESCAPE '\' OR field_path LIKE '/outcome/defects/%');

  SELECT decision_kind,inputs,outcome INTO v_kind,v_inputs,v_outcome
  FROM research_decision WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_decision_id;
  IF NOT FOUND OR v_kind NOT IN ('quality_gate','citation_audit') THEN RETURN 0; END IF;
  v_report_text:=v_inputs->>'report_id';
  IF NOT research_artifact_reference_uuid_valid(v_report_text) OR NOT EXISTS(
    SELECT 1 FROM research_report WHERE workspace_id=p_workspace_id
      AND session_id=p_session_id AND id=v_report_text::uuid
  ) THEN
    RETURN 0;
  END IF;
  v_report_id:=v_report_text::uuid;
  PERFORM research_artifact_diagnose_evaluation_local_array(p_workspace_id,p_session_id,p_decision_id,v_report_id,'/outcome/reviewed_claim_keys','report_claim',v_outcome->'reviewed_claim_keys');
  PERFORM research_artifact_diagnose_evaluation_local_array(p_workspace_id,p_session_id,p_decision_id,v_report_id,'/outcome/reviewed_section_ids','report_section',v_outcome->'reviewed_section_ids');
  IF v_outcome ? 'defects' AND jsonb_typeof(v_outcome->'defects')<>'array' THEN
    PERFORM research_artifact_record_migration_diagnostic(p_workspace_id,p_session_id,'evaluation_decision',p_decision_id,'/outcome/defects','evaluation_defect_array',jsonb_typeof(v_outcome->'defects'),'unknown_schema');
  ELSIF jsonb_typeof(v_outcome->'defects')='array' THEN
    FOR v_defect,v_ordinal IN SELECT value,ordinality FROM jsonb_array_elements(v_outcome->'defects') WITH ORDINALITY LOOP
      v_base:='/outcome/defects/'||(v_ordinal-1);
      IF jsonb_typeof(v_defect)<>'object' THEN
        PERFORM research_artifact_record_migration_diagnostic(p_workspace_id,p_session_id,'evaluation_decision',p_decision_id,v_base,'evaluation_defect',jsonb_typeof(v_defect),'unknown_schema');
        CONTINUE;
      END IF;
      PERFORM research_artifact_diagnose_evaluation_local_array(p_workspace_id,p_session_id,p_decision_id,v_report_id,v_base||'/claim_keys','report_claim',v_defect->'claim_keys');
      PERFORM research_artifact_diagnose_evaluation_local_array(p_workspace_id,p_session_id,p_decision_id,v_report_id,v_base||'/section_ids','report_section',v_defect->'section_ids');
    END LOOP;
  END IF;
  SELECT count(*)::int INTO v_count FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='evaluation_decision' AND owner_id=p_decision_id
    AND (field_path LIKE '/outcome/reviewed\_%' ESCAPE '\' OR field_path LIKE '/outcome/defects/%');
  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_evaluation_local_diagnostic_refresh_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM research_artifact_scan_research_evaluation_local_diagnostics(NEW.workspace_id,NEW.session_id,NEW.id);
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_decision_z_evaluation_local_diagnostic_refresh
AFTER INSERT OR UPDATE OF workspace_id,session_id,decision_kind,inputs,outcome
ON research_decision FOR EACH ROW
EXECUTE FUNCTION research_artifact_evaluation_local_diagnostic_refresh_fn();

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
    v_total:=v_total+research_artifact_scan_research_evaluation_local_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_report WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_report_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_run_event WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_run_event_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
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

DO $$ DECLARE v_decision RECORD; BEGIN
  FOR v_decision IN SELECT workspace_id,session_id,id FROM research_decision WHERE decision_kind IN ('quality_gate','citation_audit') LOOP
    PERFORM research_artifact_scan_research_evaluation_local_diagnostics(v_decision.workspace_id,v_decision.session_id,v_decision.id);
  END LOOP;
END $$;
