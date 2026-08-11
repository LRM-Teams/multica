-- AgentStartAck and AgentSession are distinct managed-launch facts. They must
-- survive server restart without being collapsed into user-facing Activity.

ALTER TABLE agent_activity_launch
  DROP CONSTRAINT agent_activity_launch_status_check;

ALTER TABLE agent_activity_launch
  ADD CONSTRAINT agent_activity_launch_status_check
  CHECK (status IN ('accepted', 'active', 'inactive'));

ALTER TABLE agent_activity_launch
  ADD COLUMN start_dispatch_id TEXT NOT NULL DEFAULT '' CHECK (length(start_dispatch_id) <= 200),
  ADD COLUMN queue_state TEXT NOT NULL DEFAULT '' CHECK (length(queue_state) <= 32),
  ADD COLUMN queue_depth INTEGER NOT NULL DEFAULT 0 CHECK (queue_depth >= 0),
  ADD COLUMN queue_age_ms BIGINT NOT NULL DEFAULT 0 CHECK (queue_age_ms >= 0),
  ADD COLUMN accepted_at TIMESTAMPTZ,
  ADD COLUMN provider_session_id TEXT NOT NULL DEFAULT '' CHECK (length(provider_session_id) <= 200),
  ADD COLUMN provider_turn_id TEXT NOT NULL DEFAULT '' CHECK (length(provider_turn_id) <= 200),
  ADD COLUMN runtime_generation BIGINT NOT NULL DEFAULT 0 CHECK (runtime_generation >= 0);

CREATE INDEX agent_activity_launch_dispatch_idx
  ON agent_activity_launch (workspace_id, start_dispatch_id)
  WHERE start_dispatch_id <> '';
