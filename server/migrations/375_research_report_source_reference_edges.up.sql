-- Chapter D §4.8: materialize exact-version lineage for structured Report sources.

CREATE OR REPLACE FUNCTION research_artifact_insert_report_source_reference(
  p_workspace_id UUID,p_session_id UUID,p_report_id UUID,p_consumer_version_id UUID,
  p_source_id TEXT,p_ordinal INTEGER
)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE v_input_version_id UUID; v_existing_ordinal INTEGER;
BEGIN
  SELECT version.id INTO v_input_version_id
  FROM research_artifact_passport passport
  JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE passport.workspace_id=p_workspace_id AND passport.session_id=p_session_id
    AND passport.id=p_source_id::uuid AND passport.entity_kind='legacy_source';
  IF v_input_version_id IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'report_revision',p_report_id,
      '/structured/sources/'||p_ordinal||'/source_id','legacy_source_version',
      p_source_id,'unresolved_reference'
    );
    RETURN;
  END IF;
  SELECT reference.ordinal INTO v_existing_ordinal
  FROM research_artifact_input_reference reference
  WHERE reference.workspace_id=p_workspace_id AND reference.session_id=p_session_id
    AND reference.consumer_version_id=p_consumer_version_id
    AND reference.input_version_id=v_input_version_id AND reference.relation='report_source';
  IF FOUND THEN
    IF v_existing_ordinal<>p_ordinal THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id,p_session_id,'report_revision',p_report_id,
        '/structured/sources/'||p_ordinal||'/source_id','legacy_source',
        p_source_id,'unknown_schema'
      );
    END IF;
    RETURN;
  END IF;
  INSERT INTO research_artifact_input_reference(
    workspace_id,session_id,consumer_version_id,input_version_id,relation,
    explicitly_used,purpose,ordinal
  ) VALUES(
    p_workspace_id,p_session_id,p_consumer_version_id,v_input_version_id,
    'report_source',true,'report_source_migration',p_ordinal
  );
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_report_source_references(
  p_workspace_id UUID,p_session_id UUID,p_report_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_structured JSONB; v_consumer UUID; v_diagnostics INTEGER;
  v_item JSONB; v_ordinal BIGINT; v_count INTEGER;
BEGIN
  v_diagnostics:=research_artifact_scan_research_report_migration_diagnostics(
    p_workspace_id,p_session_id,p_report_id
  );
  SELECT report.structured,version.id INTO v_structured,v_consumer
  FROM research_report report
  LEFT JOIN research_artifact_passport passport
    ON passport.workspace_id=report.workspace_id AND passport.session_id=report.session_id
   AND passport.id=report.id AND passport.entity_kind='report_revision'
  LEFT JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE report.workspace_id=p_workspace_id AND report.session_id=p_session_id AND report.id=p_report_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  IF v_consumer IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'report_revision',p_report_id,'/artifact_version',
      'report_revision_version',p_report_id::text,'unresolved_reference'
    );
    RETURN v_diagnostics+1;
  END IF;
  IF v_structured IS NULL OR v_structured='{}'::jsonb OR v_diagnostics>0 THEN
    RETURN v_diagnostics;
  END IF;
  FOR v_item,v_ordinal IN
    SELECT value,ordinality
    FROM jsonb_array_elements(v_structured->'sources') WITH ORDINALITY
  LOOP
    PERFORM research_artifact_insert_report_source_reference(
      p_workspace_id,p_session_id,p_report_id,v_consumer,
      v_item->>'source_id',(v_ordinal-1)::integer
    );
  END LOOP;
  SELECT count(*)::int INTO v_count FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='report_revision' AND owner_id=p_report_id;
  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_session_migration_diagnostics(
  p_workspace_id UUID,p_session_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_owner_id UUID; v_total INTEGER := 0; v_scanned INTEGER;
BEGIN
  FOR v_owner_id IN SELECT id FROM research_message WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    IF to_regprocedure('research_artifact_materialize_message_match_references(uuid,uuid,uuid)') IS NOT NULL THEN
      EXECUTE 'SELECT research_artifact_materialize_message_match_references($1,$2,$3)' INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total:=v_total+v_scanned;
    ELSE
      v_total:=v_total+research_artifact_scan_research_message_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
    END IF;
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_decision WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_decision_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
    IF to_regprocedure('research_artifact_scan_research_evaluation_local_diagnostics(uuid,uuid,uuid)') IS NOT NULL THEN
      EXECUTE 'SELECT research_artifact_scan_research_evaluation_local_diagnostics($1,$2,$3)' INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total:=v_total+v_scanned;
    END IF;
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_report WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    IF to_regprocedure('research_artifact_materialize_report_source_references(uuid,uuid,uuid)') IS NOT NULL THEN
      EXECUTE 'SELECT research_artifact_materialize_report_source_references($1,$2,$3)' INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total:=v_total+v_scanned;
    ELSE
      v_total:=v_total+research_artifact_scan_research_report_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
    END IF;
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

DO $$ DECLARE v_report RECORD; BEGIN
  FOR v_report IN SELECT workspace_id,session_id,id FROM research_report LOOP
    PERFORM research_artifact_materialize_report_source_references(
      v_report.workspace_id,v_report.session_id,v_report.id
    );
  END LOOP;
END $$;
