-- Record WHY a Goal changed, not only what changed. The live column holds the
-- reason for the current revision only; every revision snapshot carries the
-- reason it was created with, so "v3 -> v4 because the customer dropped the
-- export requirement" survives in the history.
ALTER TABLE channel_goal
  ADD COLUMN change_reason TEXT NOT NULL DEFAULT ''
    CHECK (length(change_reason) <= 2000);

ALTER TABLE channel_goal_revision
  ADD COLUMN change_reason TEXT NOT NULL DEFAULT '';

CREATE OR REPLACE FUNCTION snapshot_channel_goal_revision()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO channel_goal_revision (
    goal_id, workspace_id, channel_id, version, title, objective,
    success_criteria, status, progress_summary, current_step, blocker,
    evidence_refs, completed_criteria, changed_by_type, changed_by_id,
    change_reason
  ) VALUES (
    NEW.id, NEW.workspace_id, NEW.channel_id, NEW.version, NEW.title,
    NEW.objective, NEW.success_criteria, NEW.status, NEW.progress_summary,
    NEW.current_step, NEW.blocker, NEW.evidence_refs, NEW.completed_criteria,
    NEW.updated_by_type, NEW.updated_by_id, NEW.change_reason
  ) ON CONFLICT (goal_id, version) DO NOTHING;
  RETURN NEW;
END;
$$;
