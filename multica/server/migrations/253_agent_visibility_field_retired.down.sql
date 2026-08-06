-- Restores the schema-only shape from before 253 (columns + CHECK
-- constraints), matching the original definitions in 001_init.up.sql
-- (agent.visibility) and 140_agent_creation_drafts.up.sql
-- (agent_creation_draft.visibility). Does not restore historical values —
-- every row gets the column's default ('workspace' / 'private' respectively),
-- not whatever it held before 253 ran.

ALTER TABLE agent ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'workspace';
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_visibility_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_visibility_check
  CHECK (visibility IN ('workspace', 'private'));

ALTER TABLE agent_creation_draft ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'private';
ALTER TABLE agent_creation_draft DROP CONSTRAINT IF EXISTS agent_creation_draft_visibility_check;
ALTER TABLE agent_creation_draft
  ADD CONSTRAINT agent_creation_draft_visibility_check
  CHECK (visibility IN ('workspace', 'private'));

-- Restore journal_workspace_radar_agent to its exact pre-253 body (verbatim
-- from 173_wendy_persona_refresh_radar_guard.up.sql), including the
-- OLD.visibility comparison the column being restored above makes valid again.
CREATE OR REPLACE FUNCTION journal_workspace_radar_agent()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value agent%ROWTYPE;
  display_label TEXT;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  IF EXISTS (
    SELECT 1 FROM workspace_radar_state state
    WHERE state.workspace_id = row_value.workspace_id
      AND state.supervisor_agent_id = row_value.id
  ) THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;

  IF TG_OP = 'UPDATE' THEN
    display_label := COALESCE(NULLIF(NEW.display_name, ''), NEW.name);
    IF OLD.name IS NOT DISTINCT FROM NEW.name
       AND OLD.display_name IS NOT DISTINCT FROM NEW.display_name
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.runtime_id IS NOT DISTINCT FROM NEW.runtime_id
       AND OLD.visibility IS NOT DISTINCT FROM NEW.visibility
       AND OLD.owner_id IS NOT DISTINCT FROM NEW.owner_id
       AND OLD.archived_at IS NOT DISTINCT FROM NEW.archived_at
       AND OLD.max_concurrent_tasks IS NOT DISTINCT FROM NEW.max_concurrent_tasks
       AND (
         OLD.description IS NOT DISTINCT FROM NEW.description
         OR display_label IN ('Wendy', 'Joe')
       ) THEN
      RETURN NEW;
    END IF;
  END IF;

  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'agent', row_value.id, clock_timestamp(),
    'agent', row_value.id,
    jsonb_build_object(
      'agent_id', row_value.id,
      'name', COALESCE(NULLIF(row_value.display_name, ''), row_value.name),
      'status', CASE WHEN TG_OP = 'DELETE' THEN 'deleted' ELSE row_value.status END,
      'runtime_id', row_value.runtime_id,
      'archived_at', row_value.archived_at,
      'capabilities', left(COALESCE(row_value.description, ''), 300)
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;
