-- Chapter D §15.4: current-version publication and access transformation are
-- distinct policy mutations over the same passport/version transition.

CREATE OR REPLACE FUNCTION research_artifact_current_version_to_policy_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.current_version IS NOT DISTINCT FROM OLD.current_version THEN
    RETURN NEW;
  END IF;

  IF OLD.current_version IS NULL
     AND NEW.current_version = 1
     AND OLD.eligibility_revision = 1
     AND NEW.eligibility_revision = 1 THEN
    RETURN NEW;
  END IF;

  IF OLD.current_version IS NULL
     OR NEW.current_version IS NULL
     OR NEW.eligibility_revision <> OLD.eligibility_revision + 1
     OR NOT EXISTS (
       SELECT 1
       FROM research_artifact_policy_mutation m
       WHERE m.workspace_id = NEW.workspace_id
         AND m.session_id = NEW.session_id
         AND m.artifact_id = NEW.id
         AND m.old_eligibility_revision = OLD.eligibility_revision
         AND m.new_eligibility_revision = NEW.eligibility_revision
         AND m.old_current_version = OLD.current_version
         AND m.new_current_version = NEW.current_version
         AND m.watermark = research_artifact_current_policy_watermark(
           NEW.workspace_id, NEW.session_id
         )
         AND (
           (m.mutation_kind = 'current_version'
            AND m.old_access_level IS NULL
            AND m.new_access_level IS NULL
            AND (
              SELECT old_version.access_level
              FROM research_artifact_version old_version
              WHERE old_version.workspace_id = NEW.workspace_id
                AND old_version.session_id = NEW.session_id
                AND old_version.artifact_id = NEW.id
                AND old_version.version = OLD.current_version
            ) IS NOT DISTINCT FROM (
              SELECT new_version.access_level
              FROM research_artifact_version new_version
              WHERE new_version.workspace_id = NEW.workspace_id
                AND new_version.session_id = NEW.session_id
                AND new_version.artifact_id = NEW.id
                AND new_version.version = NEW.current_version
            ))
           OR
           (m.mutation_kind = 'access'
            AND m.old_access_level = (
              SELECT old_version.access_level
              FROM research_artifact_version old_version
              WHERE old_version.workspace_id = NEW.workspace_id
                AND old_version.session_id = NEW.session_id
                AND old_version.artifact_id = NEW.id
                AND old_version.version = OLD.current_version
            )
            AND m.new_access_level = (
              SELECT new_version.access_level
              FROM research_artifact_version new_version
              WHERE new_version.workspace_id = NEW.workspace_id
                AND new_version.session_id = NEW.session_id
                AND new_version.artifact_id = NEW.id
                AND new_version.version = NEW.current_version
            ))
         )
     ) THEN
    RAISE foreign_key_violation
      USING CONSTRAINT = 'research_artifact_current_version_to_policy_guard';
  END IF;

  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_policy_mutation_to_current_version_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.mutation_kind NOT IN ('current_version', 'access') THEN
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
     ) <> NEW.watermark
     OR (
       NEW.mutation_kind = 'current_version'
       AND (
         NEW.old_access_level IS NOT NULL
         OR NEW.new_access_level IS NOT NULL
         OR (
           SELECT old_version.access_level
           FROM research_artifact_version old_version
           WHERE old_version.workspace_id = NEW.workspace_id
             AND old_version.session_id = NEW.session_id
             AND old_version.artifact_id = NEW.artifact_id
             AND old_version.version = NEW.old_current_version
         ) IS DISTINCT FROM (
           SELECT new_version.access_level
           FROM research_artifact_version new_version
           WHERE new_version.workspace_id = NEW.workspace_id
             AND new_version.session_id = NEW.session_id
             AND new_version.artifact_id = NEW.artifact_id
             AND new_version.version = NEW.new_current_version
         )
       )
     )
     OR (
       NEW.mutation_kind = 'access'
       AND (
         NEW.old_access_level IS NULL
         OR NEW.new_access_level IS NULL
         OR NEW.old_access_level IS NOT DISTINCT FROM NEW.new_access_level
         OR NEW.old_access_level IS DISTINCT FROM (
           SELECT old_version.access_level
           FROM research_artifact_version old_version
           WHERE old_version.workspace_id = NEW.workspace_id
             AND old_version.session_id = NEW.session_id
             AND old_version.artifact_id = NEW.artifact_id
             AND old_version.version = NEW.old_current_version
         )
         OR NEW.new_access_level IS DISTINCT FROM (
           SELECT new_version.access_level
           FROM research_artifact_version new_version
           WHERE new_version.workspace_id = NEW.workspace_id
             AND new_version.session_id = NEW.session_id
             AND new_version.artifact_id = NEW.artifact_id
             AND new_version.version = NEW.new_current_version
         )
       )
     ) THEN
    RAISE foreign_key_violation
      USING CONSTRAINT = 'research_artifact_policy_mutation_to_current_version_guard';
  END IF;

  RETURN NEW;
END;
$$;
