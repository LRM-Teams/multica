-- Task #908 (agent visibility mechanism retirement), final cut: the
-- workspace/private distinction itself is retired. Frank, #multica thread
-- f83df812, 2026-07-30 10:55-11:01: "所有代码全部删掉，默认public的" — usage/
-- existence has been unconditional for every workspace member since batch 1;
-- the four sensitive tabs collapsed to isAdminLike(viewer) OR
-- agent.owner_id==viewer in batch 2/3. Nothing in the application layer reads
-- agent.visibility or agent_creation_draft.visibility for access-control
-- purposes anymore (see #multica thread f83df812, 2026-07-30 18:31-20:19 for
-- the last four real call sites and their individual resolutions — Windy
-- identity, onboarding-assistant lookup, private-runtime capacity matching,
-- and the aggregate-list filter — none of which needed the column itself).
--
-- No data migration needed: every row's value becomes unreadable/unwritable,
-- not incorrect: the column is simply gone.

-- journal_workspace_radar_agent (last redefined in 173_wendy_persona_refresh_radar_guard)
-- compares OLD.visibility IS NOT DISTINCT FROM NEW.visibility to decide whether
-- an UPDATE is a no-op for Radar journaling purposes. That comparison must go
-- before the column does, or every UPDATE on `agent` fails at the trigger
-- with "record has no field visibility".
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

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_visibility_check;
ALTER TABLE agent DROP COLUMN IF EXISTS visibility;

ALTER TABLE agent_creation_draft DROP CONSTRAINT IF EXISTS agent_creation_draft_visibility_check;
ALTER TABLE agent_creation_draft DROP COLUMN IF EXISTS visibility;

-- Canary: DROP COLUMN succeeds even when a plpgsql trigger body still
-- references OLD.visibility/NEW.visibility, because that reference is plain
-- text, not a tracked dependency (unlike a view, index, or FK) — so this
-- migration would go green while a live trigger silently breaks the first
-- time anyone updates a row. Force an UPDATE here (no-op if the agent table
-- is empty) so any such trigger fails loudly in the migration itself instead
-- of the first production UPDATE agent after deploy.
UPDATE agent SET updated_at = updated_at WHERE id = (SELECT id FROM agent LIMIT 1);
