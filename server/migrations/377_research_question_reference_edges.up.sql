-- Chapter D §4.8: backfill exact-version Question parent, creator, and answer lineage.

CREATE OR REPLACE FUNCTION research_artifact_insert_question_reference(
  p_workspace_id UUID,p_session_id UUID,p_question_id UUID,p_input_id UUID,
  p_input_kind TEXT,p_relation TEXT,p_field_path TEXT
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_consumer UUID; v_input UUID; v_existing_ordinal INTEGER;
BEGIN
  SELECT version.id INTO v_consumer
  FROM research_artifact_passport passport
  JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE passport.workspace_id=p_workspace_id AND passport.session_id=p_session_id
    AND passport.id=p_question_id AND passport.entity_kind='question';
  IF v_consumer IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'question',p_question_id,'/artifact_version',
      'question_version',p_question_id::text,'unresolved_reference'
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
      p_workspace_id,p_session_id,'question',p_question_id,p_field_path,
      p_input_kind||'_version',p_input_id::text,'unresolved_reference'
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
        p_workspace_id,p_session_id,'question',p_question_id,p_field_path,
        p_input_kind,p_input_id::text,'unknown_schema'
      );
      RETURN 1;
    END IF;
    RETURN 0;
  END IF;
  INSERT INTO research_artifact_input_reference(
    workspace_id,session_id,consumer_version_id,input_version_id,relation,
    explicitly_used,purpose,ordinal
  ) VALUES(
    p_workspace_id,p_session_id,v_consumer,v_input,p_relation,true,
    'question_relationship_migration',0
  );
  RETURN 0;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_question_references(
  p_workspace_id UUID,p_session_id UUID,p_question_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_parent UUID; v_creator UUID; v_answer UUID; v_total INTEGER := 0; v_cycle BOOLEAN;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'question',p_question_id
  );
  SELECT question.parent_question_id,question.created_by_task_id,question.answer_claim_id
  INTO v_parent,v_creator,v_answer
  FROM research_question question
  WHERE question.workspace_id=p_workspace_id AND question.session_id=p_session_id
    AND question.id=p_question_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  WITH RECURSIVE ancestors(id,path,cycle) AS (
    SELECT v_parent,ARRAY[p_question_id,v_parent],v_parent=p_question_id
    WHERE v_parent IS NOT NULL
    UNION ALL
    SELECT question.parent_question_id,ancestors.path||question.parent_question_id,
           question.parent_question_id=ANY(ancestors.path)
    FROM ancestors
    JOIN research_question question
      ON question.workspace_id=p_workspace_id AND question.session_id=p_session_id
     AND question.id=ancestors.id
    WHERE NOT ancestors.cycle AND question.parent_question_id IS NOT NULL
  )
  SELECT COALESCE(bool_or(cycle),false) INTO v_cycle FROM ancestors;
  IF v_cycle THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'question',p_question_id,'/parent_question_id',
      'question',v_parent::text,'cyclic_local_reference'
    );
    RETURN 1;
  END IF;
  IF v_parent IS NOT NULL THEN
    v_total:=v_total+research_artifact_insert_question_reference(
      p_workspace_id,p_session_id,p_question_id,v_parent,'question',
      'question_parent','/parent_question_id'
    );
  END IF;
  IF v_creator IS NOT NULL THEN
    v_total:=v_total+research_artifact_insert_question_reference(
      p_workspace_id,p_session_id,p_question_id,v_creator,'task',
      'created_by_task','/created_by_task_id'
    );
  END IF;
  IF v_answer IS NOT NULL THEN
    v_total:=v_total+research_artifact_insert_question_reference(
      p_workspace_id,p_session_id,p_question_id,v_answer,'claim',
      'answer_claim','/answer_claim_id'
    );
  END IF;
  RETURN v_total;
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
  FOR v_owner_id IN SELECT id FROM research_question WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_materialize_question_references(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  RETURN v_total;
END;
$$;

DO $$ DECLARE v_question RECORD; BEGIN
  FOR v_question IN SELECT workspace_id,session_id,id FROM research_question LOOP
    PERFORM research_artifact_materialize_question_references(
      v_question.workspace_id,v_question.session_id,v_question.id
    );
  END LOOP;
END $$;
