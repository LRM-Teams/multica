-- Chapter D §4.8: backfill exact-version Source projection and Observation lineage.

CREATE OR REPLACE FUNCTION research_artifact_insert_source_snapshot_reference(
  p_workspace_id UUID,p_session_id UUID,p_owner_kind TEXT,p_owner_id UUID,
  p_input_id UUID,p_relation TEXT,p_purpose TEXT,p_field_path TEXT
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_consumer UUID; v_input UUID; v_existing_ordinal INTEGER;
BEGIN
  SELECT version.id INTO v_consumer
  FROM research_artifact_passport passport
  JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE passport.workspace_id=p_workspace_id AND passport.session_id=p_session_id
    AND passport.id=p_owner_id AND passport.entity_kind=p_owner_kind;
  IF v_consumer IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,'/artifact_version',
      p_owner_kind||'_version',p_owner_id::text,'unresolved_reference'
    );
    RETURN 1;
  END IF;
  SELECT version.id INTO v_input
  FROM research_artifact_passport passport
  JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE passport.workspace_id=p_workspace_id AND passport.session_id=p_session_id
    AND passport.id=p_input_id AND passport.entity_kind='source_snapshot';
  IF v_input IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
      'source_snapshot_version',p_input_id::text,'unresolved_reference'
    );
    RETURN 1;
  END IF;
  SELECT reference.ordinal INTO v_existing_ordinal
  FROM research_artifact_input_reference reference
  WHERE reference.workspace_id=p_workspace_id AND reference.session_id=p_session_id
    AND reference.consumer_version_id=v_consumer
    AND reference.input_version_id=v_input AND reference.relation=p_relation;
  IF FOUND THEN
    IF v_existing_ordinal<>0 THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
        'source_snapshot',p_input_id::text,'unknown_schema'
      );
      RETURN 1;
    END IF;
    RETURN 0;
  END IF;
  INSERT INTO research_artifact_input_reference(
    workspace_id,session_id,consumer_version_id,input_version_id,relation,
    explicitly_used,purpose,ordinal
  ) VALUES(
    p_workspace_id,p_session_id,v_consumer,v_input,p_relation,true,p_purpose,0
  );
  RETURN 0;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_legacy_source_reference(
  p_workspace_id UUID,p_session_id UUID,p_source_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_snapshot_id UUID; v_diagnostics INTEGER;
BEGIN
  v_diagnostics:=research_artifact_scan_research_legacy_source_migration_diagnostics(
    p_workspace_id,p_session_id,p_source_id
  );
  SELECT source.source_snapshot_id INTO v_snapshot_id
  FROM research_source source
  WHERE source.workspace_id=p_workspace_id AND source.session_id=p_session_id AND source.id=p_source_id;
  IF NOT FOUND OR v_snapshot_id IS NULL OR v_diagnostics>0 THEN RETURN v_diagnostics; END IF;
  RETURN v_diagnostics+research_artifact_insert_source_snapshot_reference(
    p_workspace_id,p_session_id,'legacy_source',p_source_id,v_snapshot_id,
    'projects','source_projection_migration','/source_snapshot_id'
  );
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_observation_reference(
  p_workspace_id UUID,p_session_id UUID,p_observation_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_snapshot_id UUID;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'observation',p_observation_id
  );
  SELECT observation.source_snapshot_id INTO v_snapshot_id
  FROM research_observation observation
  WHERE observation.workspace_id=p_workspace_id AND observation.session_id=p_session_id
    AND observation.id=p_observation_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  RETURN research_artifact_insert_source_snapshot_reference(
    p_workspace_id,p_session_id,'observation',p_observation_id,v_snapshot_id,
    'observes','observation_migration','/source_snapshot_id'
  );
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
  FOR v_owner_id IN SELECT id FROM research_source WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_materialize_legacy_source_reference(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_observation WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_materialize_observation_reference(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  RETURN v_total;
END;
$$;

DO $$ DECLARE v_owner RECORD; BEGIN
  FOR v_owner IN SELECT workspace_id,session_id,id FROM research_source LOOP
    PERFORM research_artifact_materialize_legacy_source_reference(v_owner.workspace_id,v_owner.session_id,v_owner.id);
  END LOOP;
  FOR v_owner IN SELECT workspace_id,session_id,id FROM research_observation LOOP
    PERFORM research_artifact_materialize_observation_reference(v_owner.workspace_id,v_owner.session_id,v_owner.id);
  END LOOP;
END $$;
