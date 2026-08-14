-- Chapter D §15.8: Manifest selection rows are assembled once in the same
-- transaction as the Manifest header, then sealed. Result lineage may add new
-- input-reference rows later, but no persisted lineage row is mutable.

CREATE OR REPLACE FUNCTION research_artifact_manifest_assembly_marker_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  CREATE TEMP TABLE IF NOT EXISTS research_artifact_manifest_assembly_marker (
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    manifest_id UUID NOT NULL,
    PRIMARY KEY (workspace_id, session_id, manifest_id)
  ) ON COMMIT DROP;

  INSERT INTO research_artifact_manifest_assembly_marker (
    workspace_id, session_id, manifest_id
  ) VALUES (NEW.workspace_id, NEW.session_id, NEW.id)
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_artifact_manifest_assembly_marker
AFTER INSERT ON research_artifact_context_manifest
FOR EACH ROW EXECUTE FUNCTION research_artifact_manifest_assembly_marker_fn();

CREATE OR REPLACE FUNCTION research_artifact_manifest_selection_insert_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  CREATE TEMP TABLE IF NOT EXISTS research_artifact_manifest_assembly_marker (
    workspace_id UUID NOT NULL,
    session_id UUID NOT NULL,
    manifest_id UUID NOT NULL,
    PRIMARY KEY (workspace_id, session_id, manifest_id)
  ) ON COMMIT DROP;

  IF NOT EXISTS (
    SELECT 1
    FROM research_artifact_manifest_assembly_marker marker
    WHERE marker.workspace_id = NEW.workspace_id
      AND marker.session_id = NEW.session_id
      AND marker.manifest_id = NEW.manifest_id
  ) THEN
    RAISE EXCEPTION 'research artifact Manifest selection is sealed'
      USING ERRCODE = '55000', CONSTRAINT = TG_NAME;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_artifact_context_entry_insert_guard
BEFORE INSERT ON research_artifact_context_entry
FOR EACH ROW EXECUTE FUNCTION research_artifact_manifest_selection_insert_guard_fn();

CREATE TRIGGER research_artifact_context_omission_insert_guard
BEFORE INSERT ON research_artifact_context_omission
FOR EACH ROW EXECUTE FUNCTION research_artifact_manifest_selection_insert_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_manifest_lineage_update_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'research artifact Manifest lineage is immutable'
    USING ERRCODE = '55000', CONSTRAINT = TG_NAME;
END;
$$;

CREATE TRIGGER research_artifact_context_entry_update_guard
BEFORE UPDATE ON research_artifact_context_entry
FOR EACH ROW EXECUTE FUNCTION research_artifact_manifest_lineage_update_guard_fn();

CREATE TRIGGER research_artifact_context_omission_update_guard
BEFORE UPDATE ON research_artifact_context_omission
FOR EACH ROW EXECUTE FUNCTION research_artifact_manifest_lineage_update_guard_fn();

CREATE TRIGGER research_artifact_input_reference_update_guard
BEFORE UPDATE ON research_artifact_input_reference
FOR EACH ROW EXECUTE FUNCTION research_artifact_manifest_lineage_update_guard_fn();
