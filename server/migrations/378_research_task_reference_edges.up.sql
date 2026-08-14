-- Chapter D §4.8: backfill exact-version Task relational and remediation lineage.

CREATE OR REPLACE FUNCTION research_artifact_insert_task_reference(
  p_workspace_id UUID,p_session_id UUID,p_task_id UUID,p_input_id UUID,
  p_input_kind TEXT,p_relation TEXT,p_purpose TEXT,p_ordinal INTEGER,p_field_path TEXT
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
    AND passport.id=p_task_id AND passport.entity_kind='task';
  IF v_consumer IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'task',p_task_id,'/artifact_version',
      'task_version',p_task_id::text,'unresolved_reference'
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
      p_workspace_id,p_session_id,'task',p_task_id,p_field_path,
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
    p_workspace_id,p_session_id,v_consumer,v_input,p_relation,true,p_purpose,p_ordinal
  );
  RETURN 0;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_task_references(
  p_workspace_id UUID,p_session_id UUID,p_task_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_question UUID; v_parent UUID; v_goal INTEGER; v_plan INTEGER;
  v_client_key TEXT; v_criteria JSONB; v_diagnostics INTEGER; v_total INTEGER := 0;
  v_dependency RECORD; v_finding JSONB; v_metadata JSONB; v_ordinal BIGINT;
  v_target UUID; v_base TEXT; v_cycle BOOLEAN;
BEGIN
  v_diagnostics:=research_artifact_scan_research_task_migration_diagnostics(
    p_workspace_id,p_session_id,p_task_id
  );
  SELECT task.question_id,task.parent_task_id,task.goal_version,task.plan_version,
         task.client_key,task.acceptance_criteria
  INTO v_question,v_parent,v_goal,v_plan,v_client_key,v_criteria
  FROM research_task task
  WHERE task.workspace_id=p_workspace_id AND task.session_id=p_session_id AND task.id=p_task_id;
  IF NOT FOUND OR v_diagnostics>0 THEN RETURN v_diagnostics; END IF;
  WITH RECURSIVE parents(id,path,cycle) AS (
    SELECT v_parent,ARRAY[p_task_id,v_parent],v_parent=p_task_id WHERE v_parent IS NOT NULL
    UNION ALL
    SELECT task.parent_task_id,parents.path||task.parent_task_id,
           task.parent_task_id=ANY(parents.path)
    FROM parents JOIN research_task task
      ON task.workspace_id=p_workspace_id AND task.session_id=p_session_id AND task.id=parents.id
    WHERE NOT parents.cycle AND task.parent_task_id IS NOT NULL
  ) SELECT COALESCE(bool_or(cycle),false) INTO v_cycle FROM parents;
  IF v_cycle THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'task',p_task_id,'/parent_task_id',
      'task',v_parent::text,'cyclic_local_reference'
    );
    RETURN 1;
  END IF;
  WITH RECURSIVE dependencies(id,path,cycle) AS (
    SELECT dependency.depends_on_task_id,ARRAY[p_task_id,dependency.depends_on_task_id],
           dependency.depends_on_task_id=p_task_id
    FROM research_task_dependency dependency
    WHERE dependency.workspace_id=p_workspace_id AND dependency.session_id=p_session_id
      AND dependency.task_id=p_task_id
    UNION ALL
    SELECT dependency.depends_on_task_id,dependencies.path||dependency.depends_on_task_id,
           dependency.depends_on_task_id=ANY(dependencies.path)
    FROM dependencies JOIN research_task_dependency dependency
      ON dependency.workspace_id=p_workspace_id AND dependency.session_id=p_session_id
     AND dependency.task_id=dependencies.id
    WHERE NOT dependencies.cycle
  ) SELECT COALESCE(bool_or(cycle),false) INTO v_cycle FROM dependencies;
  IF v_cycle THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'task',p_task_id,'/dependencies',
      'task',p_task_id::text,'cyclic_local_reference'
    );
    RETURN 1;
  END IF;
  IF v_question IS NOT NULL THEN
    v_total:=v_total+research_artifact_insert_task_reference(
      p_workspace_id,p_session_id,p_task_id,v_question,'question',
      'task_question','task_relationship_migration',0,'/question_id'
    );
  END IF;
  IF v_parent IS NOT NULL THEN
    v_total:=v_total+research_artifact_insert_task_reference(
      p_workspace_id,p_session_id,p_task_id,v_parent,'task',
      'task_parent','task_relationship_migration',0,'/parent_task_id'
    );
  END IF;
  FOR v_dependency IN
    SELECT depends_on_task_id,row_number() OVER (ORDER BY depends_on_task_id)-1 AS ordinal
    FROM research_task_dependency
    WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND task_id=p_task_id
  LOOP
    v_total:=v_total+research_artifact_insert_task_reference(
      p_workspace_id,p_session_id,p_task_id,v_dependency.depends_on_task_id,'task',
      'task_dependency','task_relationship_migration',v_dependency.ordinal::integer,
      '/dependencies/'||v_dependency.ordinal
    );
  END LOOP;
  IF v_client_key NOT LIKE 'control:%' THEN RETURN v_total; END IF;
  FOR v_finding,v_ordinal IN
    SELECT value,ordinality FROM jsonb_array_elements(
      v_criteria#>'{remediation,target_findings}'
    ) WITH ORDINALITY
  LOOP
    v_metadata:=v_finding->'metadata';
    v_base:='/acceptance_criteria/remediation/target_findings/'||(v_ordinal-1)||'/metadata';
    IF btrim(COALESCE(v_metadata->>'question_id',''))<>'' THEN
      v_total:=v_total+research_artifact_insert_task_reference(p_workspace_id,p_session_id,p_task_id,(v_metadata->>'question_id')::uuid,'question','remediation_question','task_remediation_migration',(v_ordinal-1)::integer,v_base||'/question_id');
    END IF;
    IF btrim(COALESCE(v_metadata->>'answer_claim_id',''))<>'' THEN
      v_total:=v_total+research_artifact_insert_task_reference(p_workspace_id,p_session_id,p_task_id,(v_metadata->>'answer_claim_id')::uuid,'claim','remediation_answer_claim','task_remediation_migration',(v_ordinal-1)::integer,v_base||'/answer_claim_id');
    END IF;
    IF btrim(COALESCE(v_metadata->>'evaluation_decision_id',''))<>'' THEN
      v_total:=v_total+research_artifact_insert_task_reference(p_workspace_id,p_session_id,p_task_id,(v_metadata->>'evaluation_decision_id')::uuid,'evaluation_decision','remediation_evaluation','task_remediation_migration',(v_ordinal-1)::integer,v_base||'/evaluation_decision_id');
    END IF;
    IF btrim(COALESCE(v_metadata->>'report_id',''))<>'' THEN
      v_total:=v_total+research_artifact_insert_task_reference(p_workspace_id,p_session_id,p_task_id,(v_metadata->>'report_id')::uuid,'report_revision','remediation_report','task_remediation_migration',(v_ordinal-1)::integer,v_base||'/report_id');
    END IF;
    IF btrim(COALESCE(v_metadata->>'task_id',''))<>'' THEN
      v_total:=v_total+research_artifact_insert_task_reference(p_workspace_id,p_session_id,p_task_id,(v_metadata->>'task_id')::uuid,'task','remediation_task','task_remediation_migration',(v_ordinal-1)::integer,v_base||'/task_id');
    END IF;
    IF btrim(COALESCE(v_metadata->>'attempt_id',''))<>'' THEN
      v_total:=v_total+research_artifact_insert_task_reference(p_workspace_id,p_session_id,p_task_id,(v_metadata->>'attempt_id')::uuid,'attempt','remediation_attempt','task_remediation_migration',(v_ordinal-1)::integer,v_base||'/attempt_id');
    END IF;
    IF btrim(COALESCE(v_metadata->>'claim_key',''))<>'' THEN
      v_target:=NULL;
      SELECT claim.id INTO v_target FROM research_claim claim
      WHERE claim.workspace_id=p_workspace_id AND claim.session_id=p_session_id
        AND claim.goal_version=v_goal AND claim.plan_version=v_plan
        AND claim.client_key=v_metadata->>'claim_key';
      v_total:=v_total+research_artifact_insert_task_reference(p_workspace_id,p_session_id,p_task_id,v_target,'claim','remediation_claim_key','task_remediation_migration',(v_ordinal-1)::integer,v_base||'/claim_key');
    END IF;
    IF btrim(COALESCE(v_metadata->>'evidence_standard_key',''))<>'' THEN
      v_target:=NULL;
      SELECT decision.id INTO v_target FROM research_decision decision
      WHERE decision.workspace_id=p_workspace_id AND decision.session_id=p_session_id
        AND decision.decision_kind='research_method' AND decision.goal_version=v_goal
        AND decision.plan_version=v_plan
        AND EXISTS (SELECT 1 FROM jsonb_array_elements(decision.outcome->'evidence_standards') standard WHERE standard->>'client_key'=v_metadata->>'evidence_standard_key')
      ORDER BY decision.created_at DESC,decision.id DESC LIMIT 1;
      v_total:=v_total+research_artifact_insert_task_reference(p_workspace_id,p_session_id,p_task_id,v_target,'method_decision','remediation_evidence_standard','task_remediation_migration',(v_ordinal-1)::integer,v_base||'/evidence_standard_key');
    END IF;
  END LOOP;
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
    ELSE v_total:=v_total+research_artifact_scan_research_message_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id); END IF;
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
    ELSE v_total:=v_total+research_artifact_scan_research_report_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id); END IF;
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
  FOR v_owner_id IN SELECT id FROM research_task WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_materialize_task_references(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
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

DO $$ DECLARE v_task RECORD; BEGIN
  FOR v_task IN SELECT workspace_id,session_id,id FROM research_task LOOP
    PERFORM research_artifact_materialize_task_references(v_task.workspace_id,v_task.session_id,v_task.id);
  END LOOP;
END $$;
