ALTER TABLE research_artifact_context_manifest
  ADD CONSTRAINT research_artifact_context_manifest_normal_grant_pair_check
    CHECK ((normal_grant_id IS NULL) = (normal_grant_revision IS NULL)),
  ADD CONSTRAINT research_artifact_context_manifest_evaluation_grant_pair_check
    CHECK ((evaluation_grant_id IS NULL) = (evaluation_grant_revision IS NULL)),
  ADD CONSTRAINT research_artifact_context_manifest_normal_grant_fkey
    FOREIGN KEY (workspace_id, session_id, normal_grant_id)
    REFERENCES research_artifact_policy_grant (workspace_id, session_id, id),
  ADD CONSTRAINT research_artifact_context_manifest_evaluation_grant_fkey
    FOREIGN KEY (workspace_id, session_id, evaluation_grant_id)
    REFERENCES research_artifact_policy_grant (workspace_id, session_id, id);

CREATE OR REPLACE FUNCTION research_artifact_context_manifest_grant_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_grant research_artifact_policy_grant%ROWTYPE;
BEGIN
  IF NEW.purpose = 'task_execution' AND NEW.normal_grant_id IS NULL THEN
    RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_context_manifest_grant_guard';
  END IF;

  IF NEW.normal_grant_id IS NOT NULL THEN
    SELECT * INTO v_grant
    FROM research_artifact_policy_grant
    WHERE workspace_id = NEW.workspace_id
      AND session_id = NEW.session_id
      AND id = NEW.normal_grant_id;
    IF NOT FOUND
       OR v_grant.revision <> NEW.normal_grant_revision
       OR v_grant.status <> 'active'
       OR v_grant.evaluation_private
       OR v_grant.normal_clearance IS NULL
       OR v_grant.purpose <> NEW.purpose THEN
      RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_context_manifest_grant_guard';
    END IF;
  END IF;

  IF NEW.evaluation_grant_id IS NOT NULL THEN
    SELECT * INTO v_grant
    FROM research_artifact_policy_grant
    WHERE workspace_id = NEW.workspace_id
      AND session_id = NEW.session_id
      AND id = NEW.evaluation_grant_id;
    IF NOT FOUND
       OR v_grant.revision <> NEW.evaluation_grant_revision
       OR v_grant.status <> 'active'
       OR NOT v_grant.evaluation_private
       OR v_grant.purpose <> NEW.purpose THEN
      RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_context_manifest_grant_guard';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_context_manifest_grant_guard
AFTER INSERT OR UPDATE OF normal_grant_id, normal_grant_revision,
  evaluation_grant_id, evaluation_grant_revision, purpose
ON research_artifact_context_manifest
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_context_manifest_grant_guard_fn();
