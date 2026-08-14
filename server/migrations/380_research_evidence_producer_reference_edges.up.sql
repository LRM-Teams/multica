-- Chapter D §4.8: backfill Source Snapshot and Observation producer lineage.

CREATE OR REPLACE FUNCTION research_artifact_insert_evidence_producer_reference(
  p_workspace_id UUID,p_session_id UUID,p_owner_kind TEXT,p_owner_id UUID,
  p_task_id UUID,p_relation TEXT,p_purpose TEXT
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
    AND passport.id=p_task_id AND passport.entity_kind='task';
  IF v_input IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,'/produced_by_task_id',
      'task_version',p_task_id::text,'unresolved_reference'
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

CREATE OR REPLACE FUNCTION research_artifact_materialize_source_snapshot_producer(
  p_workspace_id UUID,p_session_id UUID,p_source_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_task UUID;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'source_snapshot',p_source_id
  );
  SELECT source.produced_by_task_id INTO v_task
  FROM research_source_snapshot source
  WHERE source.workspace_id=p_workspace_id AND source.session_id=p_session_id AND source.id=p_source_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  RETURN research_artifact_insert_evidence_producer_reference(
    p_workspace_id,p_session_id,'source_snapshot',p_source_id,v_task,
    'source_producer','source_relationship_migration'
  );
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_observation_producer(
  p_workspace_id UUID,p_session_id UUID,p_observation_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_task UUID; v_diagnostics INTEGER;
BEGIN
  SELECT count(*)::int INTO v_diagnostics FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='observation' AND owner_id=p_observation_id;
  IF v_diagnostics>0 THEN RETURN 0; END IF;
  SELECT observation.produced_by_task_id INTO v_task
  FROM research_observation observation
  WHERE observation.workspace_id=p_workspace_id AND observation.session_id=p_session_id
    AND observation.id=p_observation_id;
  IF NOT FOUND THEN RETURN 0; END IF;
  RETURN research_artifact_insert_evidence_producer_reference(
    p_workspace_id,p_session_id,'observation',p_observation_id,v_task,
    'observation_producer','observation_relationship_migration'
  );
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_observation_complete_references(
  p_workspace_id UUID,p_session_id UUID,p_observation_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_diagnostics INTEGER;
BEGIN
  v_diagnostics:=research_artifact_materialize_observation_reference(
    p_workspace_id,p_session_id,p_observation_id
  );
  IF v_diagnostics>0 THEN RETURN v_diagnostics; END IF;
  RETURN research_artifact_materialize_observation_producer(
    p_workspace_id,p_session_id,p_observation_id
  );
END;
$$;

ALTER FUNCTION research_artifact_scan_session_migration_diagnostics(UUID,UUID)
  RENAME TO research_artifact_scan_session_migration_diagnostics_v379;

CREATE FUNCTION research_artifact_scan_session_migration_diagnostics(
  p_workspace_id UUID,p_session_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_owner_id UUID; v_total INTEGER;
BEGIN
  v_total:=research_artifact_scan_session_migration_diagnostics_v379(
    p_workspace_id,p_session_id
  );
  FOR v_owner_id IN SELECT id FROM research_source_snapshot
    WHERE workspace_id=p_workspace_id AND session_id=p_session_id
  LOOP
    v_total:=v_total+research_artifact_materialize_source_snapshot_producer(
      p_workspace_id,p_session_id,v_owner_id
    );
  END LOOP;
  FOR v_owner_id IN SELECT id FROM research_observation
    WHERE workspace_id=p_workspace_id AND session_id=p_session_id
  LOOP
    v_total:=v_total+research_artifact_materialize_observation_producer(
      p_workspace_id,p_session_id,v_owner_id
    );
  END LOOP;
  RETURN v_total;
END;
$$;

DO $$ DECLARE v_owner RECORD; BEGIN
  FOR v_owner IN SELECT workspace_id,session_id,id FROM research_source_snapshot LOOP
    PERFORM research_artifact_materialize_source_snapshot_producer(v_owner.workspace_id,v_owner.session_id,v_owner.id);
  END LOOP;
  FOR v_owner IN SELECT workspace_id,session_id,id FROM research_observation LOOP
    PERFORM research_artifact_materialize_observation_complete_references(v_owner.workspace_id,v_owner.session_id,v_owner.id);
  END LOOP;
END $$;
