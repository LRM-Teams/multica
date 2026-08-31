-- Restore the migration 438 detach trigger body (without the stamp column).
CREATE OR REPLACE FUNCTION detach_channel_goal_issue_scope()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  UPDATE issue
  SET channel_goal_id = NULL,
      goal_required = NULL,
      execution_revision = execution_revision + 1,
      updated_at = now()
  WHERE workspace_id = OLD.workspace_id
    AND channel_goal_id = OLD.id;
  RETURN OLD;
END;
$$;

ALTER TABLE issue
  DROP COLUMN IF EXISTS goal_version_at_creation;
