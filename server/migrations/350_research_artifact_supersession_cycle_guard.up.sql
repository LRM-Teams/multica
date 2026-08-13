-- Supersession is immutable lineage and must remain acyclic (design §4.6, §11).

DO $$
BEGIN
  IF EXISTS (
    WITH RECURSIVE walk(workspace_id, session_id, origin_id, version_id) AS (
      SELECT workspace_id, session_id, successor_version_id, superseded_version_id
      FROM research_artifact_supersession
      UNION
      SELECT walk.workspace_id, walk.session_id, walk.origin_id, edge.superseded_version_id
      FROM walk
      JOIN research_artifact_supersession edge
        ON edge.workspace_id = walk.workspace_id
       AND edge.session_id = walk.session_id
       AND edge.successor_version_id = walk.version_id
    )
    SELECT 1 FROM walk WHERE origin_id = version_id
  ) THEN
    RAISE check_violation USING CONSTRAINT = 'research_artifact_supersession_cycle_guard';
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_supersession_cycle_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF EXISTS (
    WITH RECURSIVE walk(version_id) AS (
      SELECT NEW.superseded_version_id
      UNION
      SELECT edge.superseded_version_id
      FROM walk
      JOIN research_artifact_supersession edge
        ON edge.workspace_id = NEW.workspace_id
       AND edge.session_id = NEW.session_id
       AND edge.successor_version_id = walk.version_id
    )
    SELECT 1 FROM walk WHERE version_id = NEW.successor_version_id
  ) THEN
    RAISE check_violation USING CONSTRAINT = 'research_artifact_supersession_cycle_guard';
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_supersession_cycle_guard
AFTER INSERT ON research_artifact_supersession
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_supersession_cycle_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_supersession_append_only_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'research artifact supersession is append-only'
    USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_supersession_append_only_guard';
END;
$$;

CREATE TRIGGER research_artifact_supersession_append_only_guard
BEFORE UPDATE OR DELETE ON research_artifact_supersession
FOR EACH ROW EXECUTE FUNCTION research_artifact_supersession_append_only_guard_fn();
