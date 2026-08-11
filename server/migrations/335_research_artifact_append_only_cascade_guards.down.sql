-- Restore strict direct-and-cascade mutation guards.

ALTER TABLE research_artifact_input_reference
  DROP CONSTRAINT research_artifact_input_reference_manifest_fkey;
ALTER TABLE research_artifact_input_reference
  ADD CONSTRAINT research_artifact_input_reference_manifest_fkey
  FOREIGN KEY (workspace_id, session_id, manifest_id)
  REFERENCES research_artifact_context_manifest (workspace_id, session_id, id);

CREATE OR REPLACE FUNCTION research_artifact_session_still_exists(
  p_workspace_id UUID,
  p_session_id UUID
)
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
  SELECT EXISTS (
    SELECT 1 FROM research_session s
    WHERE s.workspace_id = p_workspace_id AND s.id = p_session_id
  );
$$;

ALTER TABLE research_artifact_version
  ALTER CONSTRAINT research_artifact_version_contract_fkey NOT DEFERRABLE INITIALLY IMMEDIATE,
  ALTER CONSTRAINT research_artifact_version_task_fkey NOT DEFERRABLE INITIALLY IMMEDIATE,
  ALTER CONSTRAINT research_artifact_version_attempt_fkey NOT DEFERRABLE INITIALLY IMMEDIATE;

CREATE OR REPLACE FUNCTION research_artifact_version_immutable_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'research artifact version is immutable'
    USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_version_immutable_guard';
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_policy_mutation_append_only_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'research artifact policy mutation is append-only'
    USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_policy_mutation_append_only_guard';
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_lifecycle_event_append_only_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'research artifact lifecycle event is append-only'
    USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_lifecycle_event_append_only_guard';
END;
$$;
