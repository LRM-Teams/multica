-- Roll back Chapter D1e policy ledger guards and backfill helper changes.

DROP TRIGGER IF EXISTS research_artifact_policy_mutation_to_passport_guard ON research_artifact_policy_mutation;
DROP FUNCTION IF EXISTS research_artifact_policy_mutation_to_passport_guard_fn();

DROP TRIGGER IF EXISTS research_artifact_passport_to_policy_mutation_guard ON research_artifact_passport;
DROP FUNCTION IF EXISTS research_artifact_passport_to_policy_mutation_guard_fn();

DROP FUNCTION IF EXISTS research_artifact_record_artifact_create_mutation(UUID, UUID, UUID);
DROP FUNCTION IF EXISTS research_artifact_policy_watermark_for_tx(UUID, UUID);

-- Restore D1b backfill helper without artifact_create side effect.
CREATE OR REPLACE FUNCTION research_artifact_backfill_registered(
  p_workspace_id UUID,
  p_session_id UUID,
  p_entity_id UUID,
  p_kind TEXT,
  p_source_created_at TIMESTAMPTZ,
  p_goal_version INTEGER DEFAULT NULL,
  p_plan_version INTEGER DEFAULT NULL
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
  v_hash TEXT;
BEGIN
  IF NOT research_artifact_entity_kind_allowed(p_kind) THEN
    RAISE EXCEPTION 'unknown artifact kind %', p_kind;
  END IF;
  v_hash := research_artifact_migration_content_hash(
    p_kind, p_workspace_id, p_session_id, p_entity_id
  );
  INSERT INTO research_artifact_passport (
    id, workspace_id, session_id, entity_kind, current_version, eligibility_revision,
    lifecycle_status, provenance_completeness, source_created_at, registered_at
  ) VALUES (
    p_entity_id, p_workspace_id, p_session_id, p_kind, NULL, 1,
    'registered', 'partial', p_source_created_at, now()
  )
  ON CONFLICT (workspace_id, session_id, id) DO NOTHING;

  INSERT INTO research_artifact_version (
    workspace_id, session_id, artifact_id, version, schema_name, schema_version,
    canonicalization_version, content_hash, access_level, goal_version, plan_version,
    hash_origin
  )
  SELECT
    p_workspace_id, p_session_id, p_entity_id, 1, p_kind, 'legacy-v1',
    'research-artifact-c14n-v1', v_hash, 'raw', p_goal_version, p_plan_version,
    'migration_recomputed'
  WHERE NOT EXISTS (
    SELECT 1 FROM research_artifact_version existing
    WHERE existing.workspace_id = p_workspace_id
      AND existing.session_id = p_session_id
      AND existing.artifact_id = p_entity_id
      AND existing.version = 1
  );

  UPDATE research_artifact_passport
  SET current_version = 1
  WHERE workspace_id = p_workspace_id
    AND session_id = p_session_id
    AND id = p_entity_id
    AND current_version IS NULL;
END;
$$;
