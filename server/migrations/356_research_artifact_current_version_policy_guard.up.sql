-- Chapter D §15.5: current_version selects the bytes exposed to future
-- Manifests, so publishing a new current version is an authorization mutation.

CREATE OR REPLACE FUNCTION research_artifact_current_version_to_policy_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_watermark BIGINT;
BEGIN
  IF NEW.current_version IS NOT DISTINCT FROM OLD.current_version THEN
    RETURN NEW;
  END IF;

  -- Initial passport registration installs version 1 and is already coupled
  -- to the artifact_create mutation by the generic reciprocal guards.
  IF OLD.current_version IS NULL
     AND NEW.current_version = 1
     AND OLD.eligibility_revision = 1
     AND NEW.eligibility_revision = 1 THEN
    RETURN NEW;
  END IF;

  IF OLD.current_version IS NULL
     OR NEW.current_version IS NULL
     OR NEW.eligibility_revision <> OLD.eligibility_revision + 1 THEN
    RAISE foreign_key_violation
      USING CONSTRAINT = 'research_artifact_current_version_to_policy_guard';
  END IF;

  v_watermark := research_artifact_current_policy_watermark(
    NEW.workspace_id, NEW.session_id
  );
  IF NOT EXISTS (
    SELECT 1
    FROM research_artifact_policy_mutation m
    WHERE m.workspace_id = NEW.workspace_id
      AND m.session_id = NEW.session_id
      AND m.artifact_id = NEW.id
      AND m.mutation_kind = 'current_version'
      AND m.old_eligibility_revision = OLD.eligibility_revision
      AND m.new_eligibility_revision = NEW.eligibility_revision
      AND m.old_current_version = OLD.current_version
      AND m.new_current_version = NEW.current_version
      AND m.watermark = v_watermark
  ) THEN
    RAISE foreign_key_violation
      USING CONSTRAINT = 'research_artifact_current_version_to_policy_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_current_version_to_policy_guard
AFTER UPDATE OF current_version ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_current_version_to_policy_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_policy_mutation_to_current_version_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.mutation_kind <> 'current_version' THEN
    RETURN NEW;
  END IF;

  IF NEW.artifact_id IS NULL
     OR NEW.old_current_version IS NULL
     OR NEW.new_current_version IS NULL
     OR NEW.old_current_version = NEW.new_current_version
     OR NOT EXISTS (
       SELECT 1
       FROM research_artifact_passport p
       WHERE p.workspace_id = NEW.workspace_id
         AND p.session_id = NEW.session_id
         AND p.id = NEW.artifact_id
         AND p.current_version = NEW.new_current_version
         AND p.eligibility_revision = NEW.new_eligibility_revision
     )
     OR research_artifact_current_policy_watermark(
       NEW.workspace_id, NEW.session_id
     ) <> NEW.watermark THEN
    RAISE foreign_key_violation
      USING CONSTRAINT = 'research_artifact_policy_mutation_to_current_version_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_policy_mutation_to_current_version_guard
AFTER INSERT ON research_artifact_policy_mutation
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_policy_mutation_to_current_version_guard_fn();
