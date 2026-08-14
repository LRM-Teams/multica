-- Chapter D §15.24: frozen authorization history is append-only while its
-- Research Session exists. Whole-session/workspace cascades remain allowed.

CREATE OR REPLACE FUNCTION research_artifact_frozen_history_delete_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_artifact_session_still_exists(OLD.workspace_id, OLD.session_id) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'research artifact frozen history is append-only'
    USING ERRCODE = '55000', CONSTRAINT = TG_NAME;
END;
$$;

CREATE TRIGGER research_artifact_policy_grant_delete_guard
BEFORE DELETE ON research_artifact_policy_grant
FOR EACH ROW EXECUTE FUNCTION research_artifact_frozen_history_delete_guard_fn();

CREATE TRIGGER research_artifact_context_manifest_delete_guard
BEFORE DELETE ON research_artifact_context_manifest
FOR EACH ROW EXECUTE FUNCTION research_artifact_frozen_history_delete_guard_fn();

CREATE TRIGGER research_artifact_context_entry_delete_guard
BEFORE DELETE ON research_artifact_context_entry
FOR EACH ROW EXECUTE FUNCTION research_artifact_frozen_history_delete_guard_fn();

CREATE TRIGGER research_artifact_context_omission_delete_guard
BEFORE DELETE ON research_artifact_context_omission
FOR EACH ROW EXECUTE FUNCTION research_artifact_frozen_history_delete_guard_fn();

CREATE TRIGGER research_artifact_input_reference_delete_guard
BEFORE DELETE ON research_artifact_input_reference
FOR EACH ROW EXECUTE FUNCTION research_artifact_frozen_history_delete_guard_fn();
