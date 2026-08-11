-- Chapter D1d: verification↔policy and grant↔mutation reciprocal guards (design §4.7.6–7).

CREATE UNLOGGED TABLE research_artifact_verification_tx_marker (
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  entity_id UUID NOT NULL,
  PRIMARY KEY (workspace_id, session_id, entity_id)
);

CREATE OR REPLACE FUNCTION research_artifact_current_policy_watermark(
  p_workspace_id UUID,
  p_session_id UUID
)
RETURNS BIGINT
LANGUAGE sql
STABLE
AS $$
  SELECT ps.watermark
  FROM research_artifact_policy_state ps
  WHERE ps.workspace_id = p_workspace_id
    AND ps.session_id = p_session_id;
$$;

CREATE OR REPLACE FUNCTION research_artifact_reserve_policy_watermark(
  p_workspace_id UUID,
  p_session_id UUID
)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
  v_watermark BIGINT;
BEGIN
  UPDATE research_artifact_policy_state
  SET watermark = watermark + 1, updated_at = now()
  WHERE workspace_id = p_workspace_id
    AND session_id = p_session_id
  RETURNING watermark INTO v_watermark;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'research artifact policy state missing for session'
      USING ERRCODE = '23503', CONSTRAINT = 'research_artifact_policy_state_session_fkey';
  END IF;
  RETURN v_watermark;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_verification_domain_marker_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.verification_status IS NOT DISTINCT FROM OLD.verification_status THEN
    RETURN NEW;
  END IF;
  INSERT INTO research_artifact_verification_tx_marker (workspace_id, session_id, entity_id)
  VALUES (NEW.workspace_id, NEW.session_id, NEW.id)
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_source_snapshot_verification_tx_marker
BEFORE UPDATE OF verification_status ON research_source_snapshot
FOR EACH ROW EXECUTE FUNCTION research_artifact_verification_domain_marker_fn();

CREATE TRIGGER research_observation_verification_tx_marker
BEFORE UPDATE OF verification_status ON research_observation
FOR EACH ROW EXECUTE FUNCTION research_artifact_verification_domain_marker_fn();

CREATE TRIGGER research_claim_evidence_verification_tx_marker
BEFORE UPDATE OF verification_status ON research_claim_evidence
FOR EACH ROW EXECUTE FUNCTION research_artifact_verification_domain_marker_fn();

CREATE OR REPLACE FUNCTION research_artifact_require_verification_policy_coupling(
  p_kind TEXT,
  p_workspace_id UUID,
  p_session_id UUID,
  p_entity_id UUID,
  p_old_status TEXT,
  p_new_status TEXT,
  p_constraint_name TEXT
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  v_revision BIGINT;
  v_watermark BIGINT;
BEGIN
  IF p_old_status IS NOT DISTINCT FROM p_new_status THEN
    RETURN;
  END IF;

  SELECT p.eligibility_revision, ps.watermark
  INTO v_revision, v_watermark
  FROM research_artifact_passport p
  JOIN research_artifact_policy_state ps
    ON ps.workspace_id = p.workspace_id AND ps.session_id = p.session_id
  WHERE p.workspace_id = p_workspace_id
    AND p.session_id = p_session_id
    AND p.id = p_entity_id
    AND p.entity_kind = p_kind;

  IF NOT FOUND THEN
    RAISE foreign_key_violation USING CONSTRAINT = p_constraint_name;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_policy_mutation m
    WHERE m.workspace_id = p_workspace_id
      AND m.session_id = p_session_id
      AND m.artifact_id = p_entity_id
      AND m.mutation_kind = 'verification'
      AND m.old_eligibility_revision = v_revision - 1
      AND m.new_eligibility_revision = v_revision
      AND m.watermark = v_watermark
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = p_constraint_name;
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_verification_domain_to_policy_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    RETURN NEW;
  END IF;
  IF NEW.verification_status IS NOT DISTINCT FROM OLD.verification_status THEN
    RETURN NEW;
  END IF;
  PERFORM research_artifact_require_verification_policy_coupling(
    TG_ARGV[0], NEW.workspace_id, NEW.session_id, NEW.id,
    OLD.verification_status, NEW.verification_status, TG_NAME
  );
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_source_snapshot_verification_to_policy_guard
AFTER UPDATE OF verification_status ON research_source_snapshot
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_verification_domain_to_policy_guard_fn('source_snapshot');

CREATE CONSTRAINT TRIGGER research_observation_verification_to_policy_guard
AFTER UPDATE OF verification_status ON research_observation
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_verification_domain_to_policy_guard_fn('observation');

CREATE CONSTRAINT TRIGGER research_claim_evidence_verification_to_policy_guard
AFTER UPDATE OF verification_status ON research_claim_evidence
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_verification_domain_to_policy_guard_fn('evidence_link');

CREATE OR REPLACE FUNCTION research_artifact_policy_mutation_to_verification_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_kind TEXT;
BEGIN
  IF NEW.mutation_kind <> 'verification' OR NEW.artifact_id IS NULL THEN
    RETURN NEW;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_verification_tx_marker m
    WHERE m.workspace_id = NEW.workspace_id
      AND m.session_id = NEW.session_id
      AND m.entity_id = NEW.artifact_id
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_verification_guard';
  END IF;

  SELECT entity_kind INTO v_kind
  FROM research_artifact_passport
  WHERE workspace_id = NEW.workspace_id
    AND session_id = NEW.session_id
    AND id = NEW.artifact_id;

  IF NOT FOUND THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_verification_guard';
  END IF;

  CASE v_kind
    WHEN 'source_snapshot' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_source_snapshot s
        WHERE s.workspace_id = NEW.workspace_id
          AND s.session_id = NEW.session_id
          AND s.id = NEW.artifact_id
      ) THEN
        RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_verification_guard';
      END IF;
    WHEN 'observation' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_observation o
        WHERE o.workspace_id = NEW.workspace_id
          AND o.session_id = NEW.session_id
          AND o.id = NEW.artifact_id
      ) THEN
        RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_verification_guard';
      END IF;
    WHEN 'evidence_link' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_claim_evidence e
        WHERE e.workspace_id = NEW.workspace_id
          AND e.session_id = NEW.session_id
          AND e.id = NEW.artifact_id
      ) THEN
        RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_verification_guard';
      END IF;
    ELSE
      RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_verification_guard';
  END CASE;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_passport p
    WHERE p.workspace_id = NEW.workspace_id
      AND p.session_id = NEW.session_id
      AND p.id = NEW.artifact_id
      AND p.eligibility_revision = NEW.new_eligibility_revision
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_verification_guard';
  END IF;

  IF research_artifact_current_policy_watermark(NEW.workspace_id, NEW.session_id) <> NEW.watermark THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_verification_guard';
  END IF;

  DELETE FROM research_artifact_verification_tx_marker
  WHERE workspace_id = NEW.workspace_id
    AND session_id = NEW.session_id
    AND entity_id = NEW.artifact_id;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_policy_mutation_to_verification_guard
AFTER INSERT ON research_artifact_policy_mutation
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_policy_mutation_to_verification_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_require_grant_policy_coupling(
  p_workspace_id UUID,
  p_session_id UUID,
  p_grant_id UUID,
  p_expected_kind TEXT,
  p_old_revision BIGINT,
  p_new_revision BIGINT,
  p_old_status TEXT,
  p_new_status TEXT,
  p_constraint_name TEXT
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  v_watermark BIGINT;
BEGIN
  v_watermark := research_artifact_current_policy_watermark(p_workspace_id, p_session_id);
  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_policy_mutation m
    WHERE m.workspace_id = p_workspace_id
      AND m.session_id = p_session_id
      AND m.policy_grant_id = p_grant_id
      AND m.mutation_kind = p_expected_kind
      AND m.old_grant_revision = p_old_revision
      AND m.new_grant_revision = p_new_revision
      AND m.old_grant_status IS NOT DISTINCT FROM p_old_status
      AND m.new_grant_status IS NOT DISTINCT FROM p_new_status
      AND m.watermark = v_watermark
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = p_constraint_name;
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_policy_grant_to_mutation_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    PERFORM research_artifact_require_grant_policy_coupling(
      NEW.workspace_id, NEW.session_id, NEW.id,
      'grant_create', 0, NEW.revision, NULL, NEW.status, TG_NAME
    );
    RETURN NEW;
  END IF;

  IF NEW.status IS NOT DISTINCT FROM OLD.status THEN
    RETURN NEW;
  END IF;
  IF NEW.status = 'revoked' AND OLD.status = 'active' THEN
    PERFORM research_artifact_require_grant_policy_coupling(
      NEW.workspace_id, NEW.session_id, NEW.id,
      'grant_revoke', OLD.revision, NEW.revision, OLD.status, NEW.status, TG_NAME
    );
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_policy_grant_to_mutation_guard
AFTER INSERT OR UPDATE OF status, revision ON research_artifact_policy_grant
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_policy_grant_to_mutation_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_policy_mutation_to_grant_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_grant research_artifact_policy_grant%ROWTYPE;
BEGIN
  IF NEW.policy_grant_id IS NULL THEN
    RETURN NEW;
  END IF;
  IF NEW.mutation_kind NOT IN ('grant_create', 'grant_revoke') THEN
    RETURN NEW;
  END IF;

  SELECT * INTO v_grant
  FROM research_artifact_policy_grant g
  WHERE g.workspace_id = NEW.workspace_id
    AND g.session_id = NEW.session_id
    AND g.id = NEW.policy_grant_id;

  IF NOT FOUND THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_grant_guard';
  END IF;

  IF v_grant.revision <> NEW.new_grant_revision
     OR v_grant.status IS DISTINCT FROM NEW.new_grant_status THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_grant_guard';
  END IF;

  IF research_artifact_current_policy_watermark(NEW.workspace_id, NEW.session_id) <> NEW.watermark THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_grant_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_policy_mutation_to_grant_guard
AFTER INSERT ON research_artifact_policy_mutation
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_policy_mutation_to_grant_guard_fn();
