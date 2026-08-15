-- Chapter D §4.8: backfill exact-version Run Event payload lineage.

CREATE OR REPLACE FUNCTION research_artifact_insert_run_event_reference(
  p_workspace_id UUID,p_session_id UUID,p_event_id UUID,p_input_id UUID,
  p_input_kind TEXT,p_relation TEXT,p_field_path TEXT
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_consumer UUID; v_input UUID;
BEGIN
  SELECT version.id INTO v_consumer
  FROM research_artifact_passport passport
  JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE passport.workspace_id=p_workspace_id AND passport.session_id=p_session_id
    AND passport.id=p_event_id AND passport.entity_kind='run_event';
  IF v_consumer IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'run_event',p_event_id,'/artifact_version',
      'run_event_version',p_event_id::text,'unresolved_reference'
    );
    RETURN 1;
  END IF;
  SELECT version.id INTO v_input
  FROM research_artifact_passport passport
  JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE passport.workspace_id=p_workspace_id AND passport.session_id=p_session_id
    AND passport.id=p_input_id AND passport.entity_kind=p_input_kind;
  IF v_input IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'run_event',p_event_id,p_field_path,
      p_input_kind||'_version',p_input_id::text,'unresolved_reference'
    );
    RETURN 1;
  END IF;
  IF EXISTS (
    SELECT 1 FROM research_artifact_input_reference reference
    WHERE reference.workspace_id=p_workspace_id AND reference.session_id=p_session_id
      AND reference.consumer_version_id=v_consumer
      AND reference.input_version_id=v_input AND reference.relation=p_relation
  ) THEN RETURN 0; END IF;
  INSERT INTO research_artifact_input_reference(
    workspace_id,session_id,consumer_version_id,input_version_id,relation,
    explicitly_used,purpose,ordinal
  ) VALUES(
    p_workspace_id,p_session_id,v_consumer,v_input,p_relation,true,
    'run_event_relationship_migration',0
  );
  RETURN 0;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_run_event_references_if_clean(
  p_workspace_id UUID,p_session_id UUID,p_event_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_payload JSONB; v_diagnostics INTEGER; v_total INTEGER := 0;
BEGIN
  SELECT count(*)::int INTO v_diagnostics FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='run_event' AND owner_id=p_event_id;
  IF v_diagnostics>0 THEN RETURN 0; END IF;
  SELECT event.payload INTO v_payload FROM research_run_event event
  WHERE event.workspace_id=p_workspace_id AND event.session_id=p_session_id AND event.id=p_event_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  v_payload:=COALESCE(v_payload,'{}'::jsonb);
  IF btrim(COALESCE(v_payload->>'task_id',''))<>'' THEN
    v_total:=v_total+research_artifact_insert_run_event_reference(p_workspace_id,p_session_id,p_event_id,(v_payload->>'task_id')::uuid,'task','event_task','/payload/task_id');
  END IF;
  IF btrim(COALESCE(v_payload->>'attempt_id',''))<>'' THEN
    v_total:=v_total+research_artifact_insert_run_event_reference(p_workspace_id,p_session_id,p_event_id,(v_payload->>'attempt_id')::uuid,'attempt','event_attempt','/payload/attempt_id');
  END IF;
  IF btrim(COALESCE(v_payload->>'question_id',''))<>'' THEN
    v_total:=v_total+research_artifact_insert_run_event_reference(p_workspace_id,p_session_id,p_event_id,(v_payload->>'question_id')::uuid,'question','event_question','/payload/question_id');
  END IF;
  IF btrim(COALESCE(v_payload->>'report_id',''))<>'' THEN
    v_total:=v_total+research_artifact_insert_run_event_reference(p_workspace_id,p_session_id,p_event_id,(v_payload->>'report_id')::uuid,'report_revision','event_report','/payload/report_id');
  END IF;
  RETURN v_total;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_run_event_references(
  p_workspace_id UUID,p_session_id UUID,p_event_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_diagnostics INTEGER;
BEGIN
  v_diagnostics:=research_artifact_scan_research_run_event_migration_diagnostics(
    p_workspace_id,p_session_id,p_event_id
  );
  IF v_diagnostics>0 THEN RETURN v_diagnostics; END IF;
  RETURN research_artifact_materialize_run_event_references_if_clean(
    p_workspace_id,p_session_id,p_event_id
  );
END;
$$;

ALTER FUNCTION research_artifact_scan_session_migration_diagnostics(UUID,UUID)
  RENAME TO research_artifact_scan_session_migration_diagnostics_v380;

CREATE FUNCTION research_artifact_scan_session_migration_diagnostics(
  p_workspace_id UUID,p_session_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_owner_id UUID; v_total INTEGER;
BEGIN
  v_total:=research_artifact_scan_session_migration_diagnostics_v380(
    p_workspace_id,p_session_id
  );
  FOR v_owner_id IN SELECT id FROM research_run_event
    WHERE workspace_id=p_workspace_id AND session_id=p_session_id
  LOOP
    v_total:=v_total+research_artifact_materialize_run_event_references_if_clean(
      p_workspace_id,p_session_id,v_owner_id
    );
  END LOOP;
  RETURN v_total;
END;
$$;

DO $$ DECLARE v_event RECORD; BEGIN
  FOR v_event IN SELECT workspace_id,session_id,id FROM research_run_event LOOP
    PERFORM research_artifact_materialize_run_event_references(
      v_event.workspace_id,v_event.session_id,v_event.id
    );
  END LOOP;
END $$;
