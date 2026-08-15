-- Chapter D §4.8: backfill exact-version Claim and Evidence Link lineage.

CREATE OR REPLACE FUNCTION research_artifact_insert_claim_evidence_reference(
  p_workspace_id UUID,p_session_id UUID,p_owner_kind TEXT,p_owner_id UUID,
  p_input_id UUID,p_input_kind TEXT,p_relation TEXT,p_purpose TEXT,p_field_path TEXT
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
    AND passport.id=p_input_id AND passport.entity_kind=p_input_kind;
  IF v_input IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
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
  ) VALUES(p_workspace_id,p_session_id,v_consumer,v_input,p_relation,true,p_purpose,0);
  RETURN 0;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_claim_references(
  p_workspace_id UUID,p_session_id UUID,p_claim_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_producer UUID; v_method UUID; v_standard TEXT; v_goal INTEGER; v_plan INTEGER;
  v_diagnostics INTEGER; v_total INTEGER := 0;
BEGIN
  v_diagnostics:=research_artifact_scan_research_claim_method_diagnostics(
    p_workspace_id,p_session_id,p_claim_id
  );
  SELECT claim.produced_by_task_id,claim.evidence_standard_key,
         claim.goal_version,claim.plan_version
  INTO v_producer,v_standard,v_goal,v_plan
  FROM research_claim claim
  WHERE claim.workspace_id=p_workspace_id AND claim.session_id=p_session_id AND claim.id=p_claim_id;
  IF NOT FOUND OR v_diagnostics>0 THEN RETURN v_diagnostics; END IF;
  v_total:=v_total+research_artifact_insert_claim_evidence_reference(
    p_workspace_id,p_session_id,'claim',p_claim_id,v_producer,'task',
    'claim_producer','claim_relationship_migration','/produced_by_task_id'
  );
  IF btrim(COALESCE(v_standard,''))<>'' THEN
    SELECT decision.id INTO v_method FROM research_decision decision
    WHERE decision.workspace_id=p_workspace_id AND decision.session_id=p_session_id
      AND decision.decision_kind='research_method' AND decision.goal_version=v_goal
      AND decision.plan_version=v_plan
      AND EXISTS (
        SELECT 1 FROM jsonb_array_elements(decision.outcome->'evidence_standards') standard
        WHERE standard->>'client_key'=v_standard
      )
    ORDER BY decision.created_at DESC,decision.id DESC LIMIT 1;
    v_total:=v_total+research_artifact_insert_claim_evidence_reference(
      p_workspace_id,p_session_id,'claim',p_claim_id,v_method,'method_decision',
      'claim_evidence_standard','claim_relationship_migration','/evidence_standard_key'
    );
  END IF;
  RETURN v_total;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_evidence_link_references(
  p_workspace_id UUID,p_session_id UUID,p_evidence_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_claim UUID; v_observation UUID; v_verifier UUID; v_total INTEGER := 0;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'evidence_link',p_evidence_id
  );
  SELECT evidence.claim_id,evidence.observation_id,evidence.verified_by_task_id
  INTO v_claim,v_observation,v_verifier
  FROM research_claim_evidence evidence
  WHERE evidence.workspace_id=p_workspace_id AND evidence.session_id=p_session_id
    AND evidence.id=p_evidence_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  v_total:=v_total+research_artifact_insert_claim_evidence_reference(
    p_workspace_id,p_session_id,'evidence_link',p_evidence_id,v_claim,'claim',
    'evidence_claim','evidence_relationship_migration','/claim_id'
  );
  v_total:=v_total+research_artifact_insert_claim_evidence_reference(
    p_workspace_id,p_session_id,'evidence_link',p_evidence_id,v_observation,'observation',
    'evidence_observation','evidence_relationship_migration','/observation_id'
  );
  IF v_verifier IS NOT NULL THEN
    v_total:=v_total+research_artifact_insert_claim_evidence_reference(
      p_workspace_id,p_session_id,'evidence_link',p_evidence_id,v_verifier,'task',
      'evidence_verifier','evidence_relationship_migration','/verified_by_task_id'
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
    v_total:=v_total+research_artifact_materialize_report_source_references(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_run_event WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_run_event_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_claim WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_materialize_claim_references(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_claim_evidence WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_materialize_evidence_link_references(p_workspace_id,p_session_id,v_owner_id);
  END LOOP;
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

DO $$ DECLARE v_owner RECORD; BEGIN
  FOR v_owner IN SELECT workspace_id,session_id,id FROM research_claim LOOP
    PERFORM research_artifact_materialize_claim_references(v_owner.workspace_id,v_owner.session_id,v_owner.id);
  END LOOP;
  FOR v_owner IN SELECT workspace_id,session_id,id FROM research_claim_evidence LOOP
    PERFORM research_artifact_materialize_evidence_link_references(v_owner.workspace_id,v_owner.session_id,v_owner.id);
  END LOOP;
END $$;
