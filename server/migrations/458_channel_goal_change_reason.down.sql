-- Restore the migration 455 trigger body (without change_reason).
CREATE OR REPLACE FUNCTION snapshot_channel_goal_revision()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO channel_goal_revision (
    goal_id, workspace_id, channel_id, version, title, objective,
    success_criteria, status, progress_summary, current_step, blocker,
    evidence_refs, completed_criteria, changed_by_type, changed_by_id
  ) VALUES (
    NEW.id, NEW.workspace_id, NEW.channel_id, NEW.version, NEW.title,
    NEW.objective, NEW.success_criteria, NEW.status, NEW.progress_summary,
    NEW.current_step, NEW.blocker, NEW.evidence_refs, NEW.completed_criteria,
    NEW.updated_by_type, NEW.updated_by_id
  ) ON CONFLICT (goal_id, version) DO NOTHING;
  RETURN NEW;
END;
$$;

ALTER TABLE channel_goal_revision
  DROP COLUMN IF EXISTS change_reason;

ALTER TABLE channel_goal
  DROP COLUMN IF EXISTS change_reason;
