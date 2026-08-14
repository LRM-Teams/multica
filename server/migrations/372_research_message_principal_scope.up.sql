-- Chapter D §4.8/§14: hard-scope Research Message principals and Run Event lineage.

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT parser IN (
    'research_message_match_decision','research_message_sender_principal',
    'research_decision_inputs','research_report_structured','research_run_event_payload',
    'research_graph_node_payload','research_legacy_source_payload',
    'research_task_remediation_acceptance_criteria',
    'research_decision_evaluation_local_references',
    'research_claim_method_evidence_standard'
  );
$$;

ALTER TABLE research_message
  DROP CONSTRAINT IF EXISTS research_message_target_agent_id_fkey,
  DROP CONSTRAINT IF EXISTS research_message_run_event_id_fkey;

ALTER TABLE research_message
  ADD CONSTRAINT research_message_target_agent_scoped_fkey
    FOREIGN KEY (workspace_id,target_agent_id)
    REFERENCES agent(workspace_id,id) ON DELETE SET NULL (target_agent_id),
  ADD CONSTRAINT research_message_run_event_scoped_fkey
    FOREIGN KEY (workspace_id,session_id,run_event_id)
    REFERENCES research_run_event(workspace_id,session_id,id) ON DELETE SET NULL (run_event_id);

CREATE OR REPLACE FUNCTION research_message_sender_principal_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  CASE NEW.sender_type
    WHEN 'system' THEN
      IF NEW.sender_id IS NOT NULL THEN
        RAISE EXCEPTION 'system Research Message sender_id must be null'
          USING ERRCODE='23514',CONSTRAINT='research_message_sender_principal_guard';
      END IF;
    WHEN 'user' THEN
      IF NEW.sender_id IS NULL OR NOT EXISTS(SELECT 1 FROM "user" WHERE id=NEW.sender_id) THEN
        RAISE EXCEPTION 'Research Message user sender does not exist'
          USING ERRCODE='23503',CONSTRAINT='research_message_sender_principal_guard';
      END IF;
    WHEN 'agent' THEN
      IF NEW.sender_id IS NULL OR NOT EXISTS(
        SELECT 1 FROM agent WHERE workspace_id=NEW.workspace_id AND id=NEW.sender_id
      ) THEN
        RAISE EXCEPTION 'Research Message Agent sender is outside the Workspace'
          USING ERRCODE='23503',CONSTRAINT='research_message_sender_principal_guard';
      END IF;
    ELSE
      RAISE EXCEPTION 'unknown Research Message sender_type %',NEW.sender_type
        USING ERRCODE='23514',CONSTRAINT='research_message_sender_principal_guard';
  END CASE;
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_message_sender_principal_guard
BEFORE INSERT OR UPDATE OF workspace_id,sender_type,sender_id ON research_message
FOR EACH ROW EXECUTE FUNCTION research_message_sender_principal_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_scan_research_message_sender_diagnostics(
  p_workspace_id UUID,p_session_id UUID,p_message_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_type TEXT; v_sender UUID; v_count INTEGER;
BEGIN
  DELETE FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='research_message' AND owner_id=p_message_id AND field_path='/sender_id';
  SELECT sender_type,sender_id INTO v_type,v_sender FROM research_message
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_message_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  IF v_type='system' AND v_sender IS NOT NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'research_message',p_message_id,'/sender_id',
      'system_sender',v_sender::text,'unknown_schema'
    );
  ELSIF v_type='user' AND (v_sender IS NULL OR NOT EXISTS(SELECT 1 FROM "user" WHERE id=v_sender)) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'research_message',p_message_id,'/sender_id',
      'user',COALESCE(v_sender::text,''),'unresolved_reference'
    );
  ELSIF v_type='agent' AND (v_sender IS NULL OR NOT EXISTS(
    SELECT 1 FROM agent WHERE workspace_id=p_workspace_id AND id=v_sender
  )) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'research_message',p_message_id,'/sender_id',
      'agent',COALESCE(v_sender::text,''),
      CASE WHEN v_sender IS NOT NULL AND EXISTS(SELECT 1 FROM agent WHERE id=v_sender)
        THEN 'cross_scope_reference' ELSE 'unresolved_reference' END
    );
  END IF;
  SELECT count(*)::int INTO v_count FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='research_message' AND owner_id=p_message_id AND field_path='/sender_id';
  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_message_diagnostic_refresh_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM research_artifact_scan_research_message_migration_diagnostics(NEW.workspace_id,NEW.session_id,NEW.id);
  PERFORM research_artifact_scan_research_message_sender_diagnostics(NEW.workspace_id,NEW.session_id,NEW.id);
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_message_relationship_diagnostic_refresh
AFTER INSERT OR UPDATE OF workspace_id,session_id,sender_type,sender_id,meta
ON research_message FOR EACH ROW
EXECUTE FUNCTION research_artifact_message_diagnostic_refresh_fn();

CREATE OR REPLACE FUNCTION research_artifact_scan_session_migration_diagnostics(
  p_workspace_id UUID,p_session_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_owner_id UUID; v_total INTEGER := 0; v_scanned INTEGER;
BEGIN
  FOR v_owner_id IN SELECT id FROM research_message WHERE workspace_id=p_workspace_id AND session_id=p_session_id LOOP
    v_total:=v_total+research_artifact_scan_research_message_migration_diagnostics(p_workspace_id,p_session_id,v_owner_id);
    IF to_regprocedure('research_artifact_scan_research_message_sender_diagnostics(uuid,uuid,uuid)') IS NOT NULL THEN
      EXECUTE 'SELECT research_artifact_scan_research_message_sender_diagnostics($1,$2,$3)' INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total:=v_total+v_scanned;
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
  FOR v_message IN SELECT workspace_id,session_id,id FROM research_message LOOP
    PERFORM research_artifact_scan_research_message_migration_diagnostics(v_message.workspace_id,v_message.session_id,v_message.id);
    PERFORM research_artifact_scan_research_message_sender_diagnostics(v_message.workspace_id,v_message.session_id,v_message.id);
  END LOOP;
END $$;
