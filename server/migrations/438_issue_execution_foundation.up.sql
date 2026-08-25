-- Additive foundation for the canonical Goal -> Issue -> Run control plane.
-- Existing Issue enqueue paths remain authoritative until the reconciler cutover.

ALTER TABLE channel_goal
  ADD COLUMN execution_graph_revision BIGINT NOT NULL DEFAULT 0
    CHECK (execution_graph_revision >= 0),
  ADD CONSTRAINT channel_goal_workspace_id_id_key UNIQUE (workspace_id, id);

ALTER TABLE issue
  ADD COLUMN channel_goal_id UUID,
  ADD COLUMN goal_required BOOLEAN,
  ADD COLUMN execution_revision BIGINT NOT NULL DEFAULT 0
    CHECK (execution_revision >= 0),
  ADD COLUMN execution_attempt_sequence BIGINT NOT NULL DEFAULT 0
    CHECK (execution_attempt_sequence >= 0),
  ADD CONSTRAINT issue_workspace_id_id_key UNIQUE (workspace_id, id),
  ADD CONSTRAINT issue_goal_scope_consistent CHECK (
    (channel_goal_id IS NULL AND goal_required IS NULL)
    OR (channel_goal_id IS NOT NULL AND goal_required IS NOT NULL)
  ),
  ADD CONSTRAINT issue_channel_goal_workspace_fkey
    FOREIGN KEY (workspace_id, channel_goal_id)
    REFERENCES channel_goal(workspace_id, id);

-- Goal deletion detaches scope atomically without leaving goal_required set on
-- an Issue that no longer has a Goal. Goal completion remains a status change.
CREATE FUNCTION detach_channel_goal_issue_scope()
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

CREATE TRIGGER channel_goal_detach_issue_scope
BEFORE DELETE ON channel_goal
FOR EACH ROW
EXECUTE FUNCTION detach_channel_goal_issue_scope();

CREATE INDEX issue_channel_goal_status_idx
  ON issue(workspace_id, channel_goal_id, status)
  WHERE channel_goal_id IS NOT NULL;

ALTER TABLE agent_inbox_event
  ADD COLUMN issue_run_kind TEXT,
  ADD COLUMN issue_execution_revision BIGINT,
  ADD COLUMN issue_execution_attempt_number BIGINT,
  ADD CONSTRAINT agent_inbox_event_issue_run_contract_check CHECK (
    (
      issue_run_kind IS NULL
      AND issue_execution_revision IS NULL
      AND issue_execution_attempt_number IS NULL
    )
    OR (
      issue_run_kind = 'canonical'
      AND issue_id IS NOT NULL
      AND reason = 'issue'
      AND issue_execution_revision >= 0
      AND issue_execution_attempt_number > 0
    )
  );

-- This index is a second fence after outbox delivery. Legacy issue wakes,
-- comments, mentions, and follow-ups have NULL issue_run_kind and can coexist.
CREATE UNIQUE INDEX agent_inbox_event_one_active_canonical_issue_run_idx
  ON agent_inbox_event(issue_id)
  WHERE issue_run_kind = 'canonical'
    AND status IN ('pending', 'draining', 'failed')
    AND terminal_outcome IS NULL;

CREATE TABLE active_issue_execution (
  issue_id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL,
  run_id UUID NOT NULL UNIQUE,
  agent_id UUID NOT NULL,
  issue_execution_revision BIGINT NOT NULL CHECK (issue_execution_revision >= 0),
  attempt_number BIGINT NOT NULL CHECK (attempt_number > 0),
  status TEXT NOT NULL DEFAULT 'dispatching'
    CHECK (status IN ('dispatching', 'active', 'releasing')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, issue_id)
    REFERENCES issue(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, agent_id)
    REFERENCES agent(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX active_issue_execution_workspace_status_idx
  ON active_issue_execution(workspace_id, status, updated_at, issue_id);

CREATE INDEX active_issue_execution_workspace_agent_idx
  ON active_issue_execution(workspace_id, agent_id);

CREATE TABLE issue_dispatch_outbox (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  issue_id UUID NOT NULL,
  run_id UUID NOT NULL UNIQUE,
  agent_id UUID NOT NULL,
  issue_execution_revision BIGINT NOT NULL CHECK (issue_execution_revision >= 0),
  attempt_number BIGINT NOT NULL CHECK (attempt_number > 0),
  dispatch_key TEXT NOT NULL UNIQUE CHECK (length(btrim(dispatch_key)) BETWEEN 1 AND 200),
  trigger_kind TEXT NOT NULL CHECK (length(btrim(trigger_kind)) BETWEEN 1 AND 80),
  request_payload JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(request_payload) = 'object'),
  request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'delivering', 'delivered', 'failed', 'cancelled')),
  delivery_attempts INTEGER NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
  next_delivery_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_token UUID,
  lease_expires_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  delivered_event_id UUID REFERENCES agent_inbox_event(id),
  delivered_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (issue_id, attempt_number),
  FOREIGN KEY (workspace_id, issue_id)
    REFERENCES issue(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, agent_id)
    REFERENCES agent(workspace_id, id) ON DELETE CASCADE,
  CHECK (
    (status = 'delivering' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
    OR (status <> 'delivering' AND lease_token IS NULL AND lease_expires_at IS NULL)
  ),
  CHECK (
    (status = 'delivered' AND delivered_event_id IS NOT NULL AND delivered_at IS NOT NULL)
    OR (status <> 'delivered' AND delivered_event_id IS NULL AND delivered_at IS NULL)
  ),
  CHECK (delivered_event_id IS NULL OR delivered_event_id = run_id)
);

CREATE INDEX issue_dispatch_outbox_due_idx
  ON issue_dispatch_outbox(next_delivery_at, created_at, id)
  WHERE status = 'pending';

CREATE INDEX issue_dispatch_outbox_expired_lease_idx
  ON issue_dispatch_outbox(lease_expires_at, id)
  WHERE status = 'delivering';

CREATE INDEX issue_dispatch_outbox_issue_history_idx
  ON issue_dispatch_outbox(issue_id, attempt_number DESC);

CREATE INDEX issue_dispatch_outbox_workspace_agent_idx
  ON issue_dispatch_outbox(workspace_id, agent_id);

CREATE INDEX issue_dispatch_outbox_delivered_event_idx
  ON issue_dispatch_outbox(delivered_event_id)
  WHERE delivered_event_id IS NOT NULL;
