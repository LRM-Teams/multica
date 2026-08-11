-- Chapter D1g: supersession↔policy and lifecycle↔policy reciprocal guards (design §4.6).

ALTER TABLE research_artifact_policy_mutation
  ADD CONSTRAINT research_artifact_policy_mutation_artifact_target_uidx
  UNIQUE (workspace_id, session_id, artifact_id, new_eligibility_revision);

ALTER TABLE research_artifact_supersession
  ADD CONSTRAINT research_artifact_supersession_policy_mutation_fkey
  FOREIGN KEY (workspace_id, session_id, superseded_artifact_id, new_eligibility_revision)
  REFERENCES research_artifact_policy_mutation (workspace_id, session_id, artifact_id, new_eligibility_revision)
  DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE research_artifact_lifecycle_event
  ADD CONSTRAINT research_artifact_lifecycle_event_policy_mutation_fkey
  FOREIGN KEY (workspace_id, session_id, artifact_id, new_eligibility_revision)
  REFERENCES research_artifact_policy_mutation (workspace_id, session_id, artifact_id, new_eligibility_revision)
  DEFERRABLE INITIALLY DEFERRED;

CREATE OR REPLACE FUNCTION research_artifact_supersession_to_policy_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_version v
    WHERE v.workspace_id = NEW.workspace_id
      AND v.session_id = NEW.session_id
      AND v.id = NEW.superseded_version_id
      AND v.artifact_id = NEW.superseded_artifact_id
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_supersession_to_policy_guard';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_passport p
    WHERE p.workspace_id = NEW.workspace_id
      AND p.session_id = NEW.session_id
      AND p.id = NEW.superseded_artifact_id
      AND p.eligibility_revision = NEW.new_eligibility_revision
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_supersession_to_policy_guard';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_policy_mutation m
    WHERE m.workspace_id = NEW.workspace_id
      AND m.session_id = NEW.session_id
      AND m.artifact_id = NEW.superseded_artifact_id
      AND m.mutation_kind = 'supersession'
      AND m.old_eligibility_revision = NEW.old_eligibility_revision
      AND m.new_eligibility_revision = NEW.new_eligibility_revision
      AND m.watermark = NEW.policy_watermark
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_supersession_to_policy_guard';
  END IF;

  IF research_artifact_current_policy_watermark(NEW.workspace_id, NEW.session_id)
     <> NEW.policy_watermark THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_supersession_to_policy_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_supersession_to_policy_guard
AFTER INSERT ON research_artifact_supersession
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_supersession_to_policy_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_policy_mutation_to_supersession_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.mutation_kind <> 'supersession' OR NEW.artifact_id IS NULL THEN
    RETURN NEW;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_supersession s
    WHERE s.workspace_id = NEW.workspace_id
      AND s.session_id = NEW.session_id
      AND s.superseded_artifact_id = NEW.artifact_id
      AND s.old_eligibility_revision = NEW.old_eligibility_revision
      AND s.new_eligibility_revision = NEW.new_eligibility_revision
      AND s.policy_watermark = NEW.watermark
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_supersession_guard';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_passport p
    WHERE p.workspace_id = NEW.workspace_id
      AND p.session_id = NEW.session_id
      AND p.id = NEW.artifact_id
      AND p.eligibility_revision = NEW.new_eligibility_revision
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_supersession_guard';
  END IF;

  IF research_artifact_current_policy_watermark(NEW.workspace_id, NEW.session_id) <> NEW.watermark THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_supersession_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_policy_mutation_to_supersession_guard
AFTER INSERT ON research_artifact_policy_mutation
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_policy_mutation_to_supersession_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_lifecycle_event_to_policy_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_passport p
    WHERE p.workspace_id = NEW.workspace_id
      AND p.session_id = NEW.session_id
      AND p.id = NEW.artifact_id
      AND p.eligibility_revision = NEW.new_eligibility_revision
      AND p.lifecycle_status = NEW.new_status
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_lifecycle_event_to_policy_guard';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_policy_mutation m
    WHERE m.workspace_id = NEW.workspace_id
      AND m.session_id = NEW.session_id
      AND m.artifact_id = NEW.artifact_id
      AND m.mutation_kind = 'lifecycle'
      AND m.old_eligibility_revision = NEW.old_eligibility_revision
      AND m.new_eligibility_revision = NEW.new_eligibility_revision
      AND m.old_lifecycle_status = NEW.old_status
      AND m.new_lifecycle_status = NEW.new_status
      AND m.watermark = NEW.policy_watermark
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_lifecycle_event_to_policy_guard';
  END IF;

  IF research_artifact_current_policy_watermark(NEW.workspace_id, NEW.session_id)
     <> NEW.policy_watermark THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_lifecycle_event_to_policy_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_lifecycle_event_to_policy_guard
AFTER INSERT ON research_artifact_lifecycle_event
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_lifecycle_event_to_policy_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_policy_mutation_to_lifecycle_event_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.mutation_kind <> 'lifecycle' OR NEW.artifact_id IS NULL THEN
    RETURN NEW;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_lifecycle_event e
    WHERE e.workspace_id = NEW.workspace_id
      AND e.session_id = NEW.session_id
      AND e.artifact_id = NEW.artifact_id
      AND e.old_eligibility_revision = NEW.old_eligibility_revision
      AND e.new_eligibility_revision = NEW.new_eligibility_revision
      AND e.old_status = NEW.old_lifecycle_status
      AND e.new_status = NEW.new_lifecycle_status
      AND e.policy_watermark = NEW.watermark
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_lifecycle_event_guard';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM research_artifact_passport p
    WHERE p.workspace_id = NEW.workspace_id
      AND p.session_id = NEW.session_id
      AND p.id = NEW.artifact_id
      AND p.eligibility_revision = NEW.new_eligibility_revision
      AND p.lifecycle_status = NEW.new_lifecycle_status
  ) THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_lifecycle_event_guard';
  END IF;

  IF research_artifact_current_policy_watermark(NEW.workspace_id, NEW.session_id) <> NEW.watermark THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_policy_mutation_to_lifecycle_event_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_policy_mutation_to_lifecycle_event_guard
AFTER INSERT ON research_artifact_policy_mutation
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_policy_mutation_to_lifecycle_event_guard_fn();
