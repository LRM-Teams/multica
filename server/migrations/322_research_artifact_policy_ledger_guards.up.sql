-- Chapter D1e: artifact_create policy ledger + generic passport↔mutation guards (design §4.5–4.7).

CREATE OR REPLACE FUNCTION research_artifact_policy_watermark_for_tx(
  p_workspace_id UUID,
  p_session_id UUID
)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  v_watermark BIGINT;
BEGIN
  CREATE TEMP TABLE IF NOT EXISTS research_artifact_policy_tx_cache (
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    watermark BIGINT NOT NULL,
    PRIMARY KEY (workspace_id, session_id)
  ) ON COMMIT DROP;

  SELECT c.watermark
  INTO v_watermark
  FROM research_artifact_policy_tx_cache c
  WHERE c.workspace_id = p_workspace_id
    AND c.session_id = p_session_id;
  IF FOUND THEN
    RETURN v_watermark;
  END IF;

  INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
  VALUES (p_workspace_id, p_session_id, 'legacy-v1-v5-compat-v1', 0)
  ON CONFLICT (workspace_id, session_id) DO NOTHING;

  v_watermark := research_artifact_reserve_policy_watermark(p_workspace_id, p_session_id);
  INSERT INTO research_artifact_policy_tx_cache (workspace_id, session_id, watermark)
  VALUES (p_workspace_id, p_session_id, v_watermark);
  RETURN v_watermark;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_record_artifact_create_mutation(
  p_workspace_id UUID,
  p_session_id UUID,
  p_artifact_id UUID
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
  v_watermark BIGINT;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_passport p
    WHERE p.workspace_id = p_workspace_id
      AND p.session_id = p_session_id
      AND p.id = p_artifact_id
      AND p.eligibility_revision = 1
  ) THEN
    RETURN;
  END IF;
  IF EXISTS (
    SELECT 1 FROM research_artifact_policy_mutation m
    WHERE m.workspace_id = p_workspace_id
      AND m.session_id = p_session_id
      AND m.artifact_id = p_artifact_id
      AND m.mutation_kind = 'artifact_create'
      AND m.old_eligibility_revision = 0
      AND m.new_eligibility_revision = 1
  ) THEN
    RETURN;
  END IF;

  v_watermark := research_artifact_policy_watermark_for_tx(p_workspace_id, p_session_id);
  INSERT INTO research_artifact_policy_mutation (
    workspace_id, session_id, watermark, mutation_kind, artifact_id,
    old_eligibility_revision, new_eligibility_revision
  ) VALUES (
    p_workspace_id, p_session_id, v_watermark, 'artifact_create', p_artifact_id, 0, 1
  );
END;
$$;

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

  PERFORM research_artifact_record_artifact_create_mutation(
    p_workspace_id, p_session_id, p_entity_id
  );
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_passport_to_policy_mutation_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_watermark BIGINT;
BEGIN
  v_watermark := research_artifact_current_policy_watermark(NEW.workspace_id, NEW.session_id);

  IF TG_OP = 'INSERT' THEN
    IF NEW.eligibility_revision <> 1 THEN
      RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_to_policy_mutation_guard';
    END IF;
    IF NOT EXISTS (
      SELECT 1 FROM research_artifact_policy_mutation m
      WHERE m.workspace_id = NEW.workspace_id
        AND m.session_id = NEW.session_id
        AND m.artifact_id = NEW.id
        AND m.mutation_kind = 'artifact_create'
        AND m.old_eligibility_revision = 0
        AND m.new_eligibility_revision = 1
        AND m.watermark = v_watermark
    ) THEN
      RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_to_policy_mutation_guard';
    END IF;
    RETURN NEW;
  END IF;

  IF NEW.eligibility_revision IS DISTINCT FROM OLD.eligibility_revision THEN
    IF NEW.eligibility_revision <> OLD.eligibility_revision + 1 THEN
      RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_to_policy_mutation_guard';
    END IF;
    IF NOT EXISTS (
      SELECT 1 FROM research_artifact_policy_mutation m
      WHERE m.workspace_id = NEW.workspace_id
        AND m.session_id = NEW.session_id
        AND m.artifact_id = NEW.id
        AND m.old_eligibility_revision = OLD.eligibility_revision
        AND m.new_eligibility_revision = NEW.eligibility_revision
        AND m.watermark = v_watermark
    ) THEN
      RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_to_policy_mutation_guard';
    END IF;
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_passport_to_policy_mutation_guard
AFTER INSERT OR UPDATE OF eligibility_revision, current_version, lifecycle_status
ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_passport_to_policy_mutation_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_policy_mutation_to_passport_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_passport research_artifact_passport%ROWTYPE;
BEGIN
  IF NEW.artifact_id IS NULL THEN
    RETURN NEW;
  END IF;

  SELECT * INTO v_passport
  FROM research_artifact_passport p
  WHERE p.workspace_id = NEW.workspace_id
    AND p.session_id = NEW.session_id
    AND p.id = NEW.artifact_id;

  IF NOT FOUND THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_passport_guard';
  END IF;

  IF NEW.new_eligibility_revision IS DISTINCT FROM v_passport.eligibility_revision THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_passport_guard';
  END IF;

  IF research_artifact_current_policy_watermark(NEW.workspace_id, NEW.session_id) <> NEW.watermark THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_passport_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_policy_mutation_to_passport_guard
AFTER INSERT ON research_artifact_policy_mutation
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_policy_mutation_to_passport_guard_fn();
