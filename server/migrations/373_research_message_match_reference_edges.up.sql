-- Chapter D §4.8: materialize exact-version lineage for legacy Message match decisions.

CREATE OR REPLACE FUNCTION research_artifact_insert_message_match_reference(
  p_workspace_id UUID,p_session_id UUID,p_message_id UUID,p_consumer_version_id UUID,
  p_field_path TEXT,p_target_id TEXT,p_target_kind TEXT,p_relation TEXT,p_ordinal INTEGER
)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE v_input_version_id UUID; v_existing_purpose TEXT; v_existing_ordinal INTEGER;
BEGIN
  IF btrim(COALESCE(p_target_id,''))='' THEN RETURN; END IF;
  SELECT version.id INTO v_input_version_id
  FROM research_artifact_passport passport
  JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE passport.workspace_id=p_workspace_id AND passport.session_id=p_session_id
    AND passport.id=p_target_id::uuid AND passport.entity_kind=p_target_kind;
  IF v_input_version_id IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'research_message',p_message_id,p_field_path,
      p_target_kind||'_version',p_target_id,'unresolved_reference'
    );
    RETURN;
  END IF;
  IF v_input_version_id=p_consumer_version_id THEN RETURN; END IF;
  SELECT reference.purpose,reference.ordinal INTO v_existing_purpose,v_existing_ordinal
  FROM research_artifact_input_reference reference
    WHERE reference.workspace_id=p_workspace_id AND reference.session_id=p_session_id
      AND reference.consumer_version_id=p_consumer_version_id
      AND reference.input_version_id=v_input_version_id AND reference.relation=p_relation;
  IF FOUND THEN
    IF v_existing_purpose='match_decision_migration' THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id,p_session_id,'research_message',p_message_id,p_field_path,
        p_target_kind,p_target_id,'duplicate_local_key'
      );
    ELSIF v_existing_ordinal<>p_ordinal THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id,p_session_id,'research_message',p_message_id,p_field_path,
        p_target_kind,p_target_id,'unknown_schema'
      );
    END IF;
    RETURN;
  END IF;
  INSERT INTO research_artifact_input_reference(
    workspace_id,session_id,consumer_version_id,input_version_id,relation,
    explicitly_used,purpose,ordinal
  ) VALUES(
    p_workspace_id,p_session_id,p_consumer_version_id,v_input_version_id,p_relation,
    true,'match_decision_migration',p_ordinal
  );
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_message_match_references(
  p_workspace_id UUID,p_session_id UUID,p_message_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_match JSONB; v_consumer UUID; v_diagnostics INTEGER; v_utterance TEXT;
  v_item JSONB; v_ordinal BIGINT; v_count INTEGER;
BEGIN
  v_diagnostics:=research_artifact_scan_research_message_migration_diagnostics(
    p_workspace_id,p_session_id,p_message_id
  );
  IF to_regprocedure('research_artifact_scan_research_message_sender_diagnostics(uuid,uuid,uuid)') IS NOT NULL THEN
    EXECUTE 'SELECT research_artifact_scan_research_message_sender_diagnostics($1,$2,$3)'
      INTO v_count USING p_workspace_id,p_session_id,p_message_id;
    v_diagnostics:=v_diagnostics+v_count;
  END IF;
  SELECT message.meta->'match_decision',version.id INTO v_match,v_consumer
  FROM research_message message
  LEFT JOIN research_artifact_passport passport
    ON passport.workspace_id=message.workspace_id AND passport.session_id=message.session_id
   AND passport.id=message.id AND passport.entity_kind='research_message'
  LEFT JOIN research_artifact_version version
    ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
       (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
  WHERE message.workspace_id=p_workspace_id AND message.session_id=p_session_id AND message.id=p_message_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  IF v_consumer IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'research_message',p_message_id,'/artifact_version',
      'research_message_version',p_message_id::text,'unresolved_reference'
    );
    RETURN v_diagnostics+1;
  END IF;
  DELETE FROM research_artifact_input_reference
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND consumer_version_id=v_consumer
    AND relation IN ('match_utterance','match_primary_anchor','match_candidate','match_decision')
    AND purpose='match_decision_migration';
  IF v_match IS NULL OR v_match='null'::jsonb OR jsonb_typeof(v_match)<>'object' OR v_diagnostics>0 THEN
    RETURN v_diagnostics;
  END IF;

  v_utterance:=btrim(COALESCE(v_match->>'utterance_id',''));
  IF v_utterance<>'' AND v_utterance<>p_message_id::text THEN
    PERFORM research_artifact_insert_message_match_reference(
      p_workspace_id,p_session_id,p_message_id,v_consumer,
      '/meta/match_decision/utterance_id',v_utterance,'research_message','match_utterance',0
    );
  END IF;
  IF btrim(COALESCE(v_match->>'primary_anchor_node_id',''))<>'' THEN
    PERFORM research_artifact_insert_message_match_reference(
      p_workspace_id,p_session_id,p_message_id,v_consumer,
      '/meta/match_decision/primary_anchor_node_id',v_match->>'primary_anchor_node_id',
      'graph_node','match_primary_anchor',0
    );
  END IF;
  FOR v_item,v_ordinal IN
    SELECT value,ordinality FROM jsonb_array_elements(COALESCE(v_match->'matched_node_ids','[]'::jsonb)) WITH ORDINALITY
  LOOP
    PERFORM research_artifact_insert_message_match_reference(
      p_workspace_id,p_session_id,p_message_id,v_consumer,
      '/meta/match_decision/matched_node_ids/'||(v_ordinal-1),v_item#>>'{}',
      'graph_node','match_candidate',(v_ordinal-1)::integer
    );
  END LOOP;
  FOR v_item,v_ordinal IN
    SELECT value,ordinality FROM jsonb_array_elements(COALESCE(v_match->'decisions','[]'::jsonb)) WITH ORDINALITY
  LOOP
    PERFORM research_artifact_insert_message_match_reference(
      p_workspace_id,p_session_id,p_message_id,v_consumer,
      '/meta/match_decision/decisions/'||(v_ordinal-1)||'/node_id',v_item->>'node_id',
      'graph_node','match_decision',(v_ordinal-1)::integer
    );
  END LOOP;
  SELECT count(*)::int INTO v_count FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='research_message' AND owner_id=p_message_id;
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

DO $$ DECLARE v_message RECORD; BEGIN
  FOR v_message IN
    SELECT workspace_id,session_id,id FROM research_message WHERE meta ? 'match_decision'
  LOOP
    PERFORM research_artifact_materialize_message_match_references(
      v_message.workspace_id,v_message.session_id,v_message.id
    );
  END LOOP;
END $$;
