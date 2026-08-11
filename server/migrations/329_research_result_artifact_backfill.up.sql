-- Chapter D completion (D1 tail): honest Result Artifact backfill for valid stored results.

CREATE OR REPLACE FUNCTION research_artifact_backfill_result_artifact(
  p_workspace_id UUID,
  p_session_id UUID,
  p_attempt_id UUID
)
RETURNS BOOLEAN
LANGUAGE plpgsql
AS $$
DECLARE
  v_result JSONB;
  v_result_hash TEXT;
  v_client_request_id TEXT;
  v_orchestrator_version TEXT;
  v_result_id UUID;
  v_content_hash TEXT;
BEGIN
  SELECT a.result, a.result_hash, COALESCE(a.client_request_id, ''),
         COALESCE(s.orchestrator_version, '')
  INTO v_result, v_result_hash, v_client_request_id, v_orchestrator_version
  FROM research_task_attempt a
  JOIN research_session s ON s.id = a.session_id
  WHERE a.workspace_id = p_workspace_id
    AND a.session_id = p_session_id
    AND a.id = p_attempt_id
    AND a.status = 'succeeded'
    AND a.result IS NOT NULL
    AND a.result <> '{}'::jsonb
    AND research_artifact_content_hash_valid(a.result_hash);

  IF NOT FOUND THEN
    RETURN false;
  END IF;

  v_result_id := gen_random_uuid();
  -- The result projection guard requires the artifact to preserve the exact
  -- hash accepted with the attempt. Recomputing a migration-only hash creates
  -- a projection that can never satisfy that invariant.
  v_content_hash := v_result_hash;

  INSERT INTO research_artifact_passport (
    id, workspace_id, session_id, entity_kind, current_version, eligibility_revision,
    lifecycle_status, provenance_completeness, source_created_at, registered_at
  ) VALUES (
    v_result_id, p_workspace_id, p_session_id, 'result_artifact', NULL, 1,
    'registered', 'partial', now(), now()
  )
  ON CONFLICT (workspace_id, session_id, id) DO NOTHING;

  INSERT INTO research_artifact_version (
    workspace_id, session_id, artifact_id, version, schema_name, schema_version,
    canonicalization_version, content_hash, access_level, hash_origin,
    produced_by_attempt_id
  )
  SELECT
    p_workspace_id, p_session_id, v_result_id, 1, 'result_artifact', 'legacy-v1',
    'research-artifact-c14n-v1', v_content_hash, 'raw', 'legacy_stored',
    p_attempt_id
  WHERE NOT EXISTS (
    SELECT 1 FROM research_artifact_version existing
    WHERE existing.workspace_id = p_workspace_id
      AND existing.session_id = p_session_id
      AND existing.artifact_id = v_result_id
      AND existing.version = 1
  );

  UPDATE research_artifact_passport
  SET current_version = 1
  WHERE workspace_id = p_workspace_id
    AND session_id = p_session_id
    AND id = v_result_id
    AND current_version IS NULL;

  INSERT INTO research_result_artifact (
    id, workspace_id, session_id, attempt_id,
    orchestrator_version, result_schema_version, result,
    client_request_id, content_hash, accepted_at
  )
  SELECT
    v_result_id, p_workspace_id, p_session_id, p_attempt_id,
    v_orchestrator_version, COALESCE(v_result->>'schema_version', '1'),
    v_result, v_client_request_id, v_content_hash, a.result_submitted_at
  FROM research_task_attempt a
  WHERE a.workspace_id = p_workspace_id
    AND a.session_id = p_session_id
    AND a.id = p_attempt_id
  ON CONFLICT (workspace_id, session_id, attempt_id) DO NOTHING;

  PERFORM research_artifact_record_artifact_create_mutation(
    p_workspace_id, p_session_id, v_result_id
  );

  RETURN true;
END;
$$;

SELECT research_artifact_backfill_result_artifact(
  a.workspace_id, a.session_id, a.id
)
FROM research_task_attempt a
WHERE a.status = 'succeeded'
  AND a.result IS NOT NULL
  AND a.result <> '{}'::jsonb
  AND research_artifact_content_hash_valid(a.result_hash)
  AND NOT EXISTS (
    SELECT 1 FROM research_result_artifact ra
    WHERE ra.workspace_id = a.workspace_id
      AND ra.session_id = a.session_id
      AND ra.attempt_id = a.id
  );
