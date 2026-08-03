-- LRM-1076: ResearchNextStep Scheduler v0
-- Unattended defaults, open-branch soft budget, work-item queue, silent-step metrics.

ALTER TABLE research_session
  ADD COLUMN IF NOT EXISTS unattended_enabled BOOLEAN NOT NULL DEFAULT true,
  ADD COLUMN IF NOT EXISTS max_open_branches INT NOT NULL DEFAULT 3,
  ADD COLUMN IF NOT EXISTS single_line_confirmed BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS unattended_auto_steps INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_user_activity_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_max_open_branches_check;
ALTER TABLE research_session
  ADD CONSTRAINT research_session_max_open_branches_check
  CHECK (max_open_branches >= 1 AND max_open_branches <= 32);

UPDATE research_session
SET last_user_activity_at = COALESCE(last_user_activity_at, created_at)
WHERE last_user_activity_at IS NULL;

CREATE TABLE IF NOT EXISTS research_work_item (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  kind TEXT NOT NULL
    CHECK (kind IN (
      'expand_subquestion',
      'evidence_gap',
      'resolve_conflict',
      'advance_probe'
    )),
  target_node_id UUID REFERENCES research_graph_node(id) ON DELETE SET NULL,
  assignee_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'enqueued', 'done', 'cancelled', 'failed')),
  reason TEXT NOT NULL DEFAULT '',
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  enqueued_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS research_work_item_session_status_idx
  ON research_work_item (session_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS research_work_item_workspace_pending_idx
  ON research_work_item (workspace_id, status, created_at ASC)
  WHERE status IN ('pending', 'enqueued');

-- Supporting index for agent hard-delete FK scans (assignee_agent_id ON DELETE SET NULL).
CREATE INDEX IF NOT EXISTS research_work_item_assignee_agent_id_idx
  ON research_work_item (assignee_agent_id)
  WHERE assignee_agent_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS research_scheduler_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  detail JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS research_scheduler_event_session_idx
  ON research_scheduler_event (session_id, created_at DESC);
