-- Chapter D1: make each policy-ledger mutation describe the exact semantic
-- passport/grant transition it claims. Eligibility revisions alone are not
-- sufficient evidence for a current-version, access, or lifecycle change.

ALTER TABLE research_artifact_policy_mutation
  ADD CONSTRAINT research_artifact_policy_mutation_fact_shape_check
  CHECK (
    CASE mutation_kind
      WHEN 'artifact_create' THEN
        artifact_id IS NOT NULL
        AND old_current_version IS NULL AND new_current_version IS NULL
        AND old_access_level IS NULL AND new_access_level IS NULL
        AND old_lifecycle_status IS NULL AND new_lifecycle_status IS NULL
      WHEN 'current_version' THEN
        artifact_id IS NOT NULL
        AND old_current_version IS NOT NULL AND new_current_version IS NOT NULL
        AND new_current_version = old_current_version + 1
        AND old_access_level IS NULL AND new_access_level IS NULL
        AND old_lifecycle_status IS NULL AND new_lifecycle_status IS NULL
      WHEN 'access' THEN
        artifact_id IS NOT NULL
        AND old_current_version IS NOT NULL AND new_current_version IS NOT NULL
        AND new_current_version = old_current_version + 1
        AND old_access_level IS NOT NULL AND new_access_level IS NOT NULL
        AND research_artifact_access_level_allowed(old_access_level)
        AND research_artifact_access_level_allowed(new_access_level)
        AND old_access_level IS DISTINCT FROM new_access_level
        AND old_lifecycle_status IS NULL AND new_lifecycle_status IS NULL
      WHEN 'lifecycle' THEN
        artifact_id IS NOT NULL
        AND old_current_version IS NULL AND new_current_version IS NULL
        AND old_access_level IS NULL AND new_access_level IS NULL
        AND old_lifecycle_status IS NOT NULL AND new_lifecycle_status IS NOT NULL
        AND research_artifact_lifecycle_status_allowed(old_lifecycle_status)
        AND research_artifact_lifecycle_status_allowed(new_lifecycle_status)
        AND old_lifecycle_status IS DISTINCT FROM new_lifecycle_status
      WHEN 'verification' THEN
        artifact_id IS NOT NULL
        AND old_current_version IS NULL AND new_current_version IS NULL
        AND old_access_level IS NULL AND new_access_level IS NULL
        AND old_lifecycle_status IS NULL AND new_lifecycle_status IS NULL
      WHEN 'supersession' THEN
        artifact_id IS NOT NULL
        AND old_current_version IS NULL AND new_current_version IS NULL
        AND old_access_level IS NULL AND new_access_level IS NULL
        AND old_lifecycle_status IS NULL AND new_lifecycle_status IS NULL
      WHEN 'eligibility' THEN
        artifact_id IS NOT NULL
        AND old_current_version IS NULL AND new_current_version IS NULL
        AND old_access_level IS NULL AND new_access_level IS NULL
        AND old_lifecycle_status IS NULL AND new_lifecycle_status IS NULL
      WHEN 'grant_create' THEN
        policy_grant_id IS NOT NULL
        AND old_grant_revision IS NOT NULL AND new_grant_revision IS NOT NULL
        AND old_grant_revision = 0 AND new_grant_revision = 1
        AND old_grant_status IS NULL AND new_grant_status = 'active'
      WHEN 'grant_revoke' THEN
        policy_grant_id IS NOT NULL
        AND old_grant_revision IS NOT NULL AND new_grant_revision IS NOT NULL
        AND new_grant_revision = old_grant_revision + 1
        AND old_grant_status = 'active' AND new_grant_status = 'revoked'
      ELSE false
    END
  ) NOT VALID;

ALTER TABLE research_artifact_policy_mutation
  VALIDATE CONSTRAINT research_artifact_policy_mutation_fact_shape_check;
