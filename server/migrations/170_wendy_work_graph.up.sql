CREATE TABLE work_node (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('issue', 'chat_commitment', 'agent_task')),
  title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 500),
  description TEXT NOT NULL DEFAULT '',
  owner_type TEXT NOT NULL CHECK (owner_type IN ('member', 'agent', 'unassigned')),
  owner_id UUID,
  status TEXT NOT NULL CHECK (status IN ('active', 'waiting', 'blocked', 'done', 'needs_rework', 'cancelled')),
  primary_channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  linked_issue_id UUID REFERENCES issue(id) ON DELETE CASCADE,
  linked_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
  last_progress_at TIMESTAMPTZ,
  last_progress_summary TEXT NOT NULL DEFAULT '',
  last_wendy_nudge_at TIMESTAMPTZ,
  last_wendy_nudge_kind TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id)
);

CREATE UNIQUE INDEX work_node_issue_uidx
  ON work_node (workspace_id, linked_issue_id)
  WHERE kind = 'issue' AND linked_issue_id IS NOT NULL;

CREATE TABLE work_edge (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  from_node_id UUID NOT NULL,
  to_node_id UUID NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('waits_on', 'blocked_by', 'rework_of')),
  status TEXT NOT NULL CHECK (status IN ('open', 'resolved')),
  evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (from_node_id <> to_node_id),
  FOREIGN KEY (workspace_id, from_node_id)
    REFERENCES work_node(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, to_node_id)
    REFERENCES work_node(workspace_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX work_edge_open_uidx
  ON work_edge (workspace_id, from_node_id, to_node_id, kind)
  WHERE status = 'open';

CREATE TABLE pending_handoff (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  urgency TEXT NOT NULL CHECK (urgency IN ('fast', 'slow')),
  reason_code TEXT NOT NULL CHECK (reason_code IN (
    'unlock', 'block_route', 'interrupt_stop', 'stalled_ask_why', 'progress_nudge'
  )),
  target_actor_type TEXT NOT NULL CHECK (target_actor_type IN ('member', 'agent')),
  target_actor_id UUID NOT NULL,
  related_node_ids UUID[] NOT NULL DEFAULT '{}',
  channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
  dedupe_key TEXT NOT NULL,
  not_before TIMESTAMPTZ NOT NULL DEFAULT now(),
  status TEXT NOT NULL CHECK (status IN ('pending', 'claimed', 'done', 'cancelled')),
  claim_token UUID,
  claimed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX pending_handoff_active_dedupe_uidx
  ON pending_handoff (workspace_id, dedupe_key)
  WHERE status IN ('pending', 'claimed');

CREATE INDEX pending_handoff_due_idx
  ON pending_handoff (urgency, reason_code, not_before, created_at)
  WHERE status = 'pending';
