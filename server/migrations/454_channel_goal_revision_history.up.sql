-- Immutable snapshots of the user-facing Goal contract.  The live
-- channel_goal row remains the fast current-state projection; this table is
-- the revision history used for audit, impact analysis, and replay.
CREATE TABLE channel_goal_revision (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    goal_id UUID NOT NULL REFERENCES channel_goal(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0),
    title TEXT NOT NULL,
    objective TEXT NOT NULL,
    success_criteria JSONB NOT NULL,
    status TEXT NOT NULL,
    progress_summary TEXT NOT NULL,
    current_step TEXT NOT NULL,
    blocker TEXT NOT NULL,
    evidence_refs JSONB NOT NULL,
    completed_criteria JSONB NOT NULL,
    changed_by_type TEXT NOT NULL,
    changed_by_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (goal_id, version)
);

CREATE INDEX channel_goal_revision_lookup
    ON channel_goal_revision (workspace_id, goal_id, version DESC);

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

CREATE TRIGGER channel_goal_revision_snapshot
AFTER INSERT OR UPDATE ON channel_goal
FOR EACH ROW EXECUTE FUNCTION snapshot_channel_goal_revision();

-- Backfill the current projection so existing goals have a revision zero-gap.
INSERT INTO channel_goal_revision (
  goal_id, workspace_id, channel_id, version, title, objective,
  success_criteria, status, progress_summary, current_step, blocker,
  evidence_refs, completed_criteria, changed_by_type, changed_by_id,
  created_at
)
SELECT id, workspace_id, channel_id, version, title, objective,
       success_criteria, status, progress_summary, current_step, blocker,
       evidence_refs, completed_criteria, updated_by_type, updated_by_id,
       updated_at
FROM channel_goal
ON CONFLICT (goal_id, version) DO NOTHING;
