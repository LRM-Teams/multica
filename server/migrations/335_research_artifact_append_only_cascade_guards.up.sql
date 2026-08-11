-- Preserve append-only semantics for direct mutation while allowing the
-- existing session/workspace ownership cascade to remove the whole tenant
-- aggregate. The reciprocal passport guards already use this boundary.

CREATE OR REPLACE FUNCTION research_artifact_session_still_exists(
  p_workspace_id UUID,
  p_session_id UUID
)
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
  SELECT EXISTS (
    SELECT 1
    FROM research_session s
    JOIN workspace w ON w.id = s.workspace_id
    WHERE s.workspace_id = p_workspace_id AND s.id = p_session_id
  );
$$;

ALTER TABLE research_artifact_input_reference
  DROP CONSTRAINT research_artifact_input_reference_manifest_fkey;
ALTER TABLE research_artifact_input_reference
  ADD CONSTRAINT research_artifact_input_reference_manifest_fkey
  FOREIGN KEY (workspace_id, session_id, manifest_id)
  REFERENCES research_artifact_context_manifest (workspace_id, session_id, id) ON DELETE CASCADE;

-- Producer lineage must remain restrictive for direct deletes, but immediate
-- checks can observe the parent side of a workspace cascade before the
-- artifact-version side is removed. Deferring the checks preserves both
-- invariants at transaction commit.
ALTER TABLE research_artifact_version
  ALTER CONSTRAINT research_artifact_version_contract_fkey DEFERRABLE INITIALLY DEFERRED,
  ALTER CONSTRAINT research_artifact_version_task_fkey DEFERRABLE INITIALLY DEFERRED,
  ALTER CONSTRAINT research_artifact_version_attempt_fkey DEFERRABLE INITIALLY DEFERRED;

CREATE OR REPLACE FUNCTION research_artifact_version_immutable_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE'
     AND NOT research_artifact_session_still_exists(OLD.workspace_id, OLD.session_id) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'research artifact version is immutable'
    USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_version_immutable_guard';
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_policy_mutation_append_only_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE'
     AND NOT research_artifact_session_still_exists(OLD.workspace_id, OLD.session_id) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'research artifact policy mutation is append-only'
    USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_policy_mutation_append_only_guard';
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_lifecycle_event_append_only_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE'
     AND NOT research_artifact_session_still_exists(OLD.workspace_id, OLD.session_id) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'research artifact lifecycle event is append-only'
    USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_lifecycle_event_append_only_guard';
END;
$$;
