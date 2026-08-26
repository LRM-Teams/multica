-- Stamp each Goal-scoped Issue with the channel_goal.version it was created
-- under. The Goal controller uses the stamp to tell the manager which open
-- Issues predate the current requirements after a Goal revision, so "user
-- changed the Goal mid-flight" becomes a visible re-validation queue instead
-- of guesswork. NULL means unknown vintage (legacy rows are backfilled below;
-- unscoped Issues stay NULL).
ALTER TABLE issue
  ADD COLUMN goal_version_at_creation BIGINT
    CHECK (goal_version_at_creation IS NULL OR goal_version_at_creation > 0);

-- Treat Issues that already exist as aligned with their Goal's current
-- requirements: the next Goal revision will correctly flag them as stale.
UPDATE issue
SET goal_version_at_creation = goal.version
FROM channel_goal goal
WHERE issue.workspace_id = goal.workspace_id
  AND issue.channel_goal_id = goal.id
  AND issue.goal_version_at_creation IS NULL;

-- Goal deletion already detaches scope (migration 438); the stamp is
-- meaningless without the Goal, so clear it in the same trigger.
CREATE OR REPLACE FUNCTION detach_channel_goal_issue_scope()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  UPDATE issue
  SET channel_goal_id = NULL,
      goal_required = NULL,
      goal_version_at_creation = NULL,
      execution_revision = execution_revision + 1,
      updated_at = now()
  WHERE workspace_id = OLD.workspace_id
    AND channel_goal_id = OLD.id;
  RETURN OLD;
END;
$$;
