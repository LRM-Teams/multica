-- PR-4: explicit collaboration sessions and turn grants. These tables are
-- intentionally issue-optional so lightweight flows (for example report-counting
-- games) do not pollute the project issue tracker, while heavier sessions can
-- attach to an issue/work graph.

CREATE TABLE collaboration_session (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
  source_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  mode TEXT NOT NULL
    CHECK (mode IN ('sequential', 'dependency', 'parallel', 'proposal', 'race')),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'active', 'completed', 'suspended', 'cancelled')),
  goal TEXT NOT NULL DEFAULT '',
  participant_agent_ids UUID[] NOT NULL,
  current_turn_index INT NOT NULL DEFAULT 0,
  expected_step TEXT NOT NULL DEFAULT '',
  completion_condition JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(completion_condition) = 'object'),
  work_graph JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(work_graph) = 'object'),
  version INT NOT NULL DEFAULT 1,
  created_by_run_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (array_length(participant_agent_ids, 1) IS NOT NULL),
  CHECK (current_turn_index >= 0),
  CHECK (version > 0)
);

CREATE INDEX idx_collaboration_session_channel_status
  ON collaboration_session(workspace_id, channel_id, status, updated_at DESC);

CREATE INDEX idx_collaboration_session_issue
  ON collaboration_session(workspace_id, issue_id)
  WHERE issue_id IS NOT NULL;

CREATE TABLE collaboration_turn (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES collaboration_session(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  inbox_event_id UUID UNIQUE REFERENCES agent_inbox_event(id) ON DELETE SET NULL,
  turn_index INT NOT NULL,
  participant_index INT NOT NULL,
  grant_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (grant_status IN ('pending', 'granted', 'consumed', 'skipped', 'expired')),
  grant_seq BIGINT,
  result_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  session_version INT NOT NULL,
  deadline_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (session_id, turn_index),
  CHECK (turn_index >= 0),
  CHECK (participant_index >= 0),
  CHECK (grant_seq IS NULL OR grant_seq >= 0),
  CHECK (session_version > 0)
);

CREATE UNIQUE INDEX idx_collaboration_turn_one_active
  ON collaboration_turn(session_id)
  WHERE grant_status IN ('pending', 'granted');

CREATE INDEX idx_collaboration_turn_inbox_event
  ON collaboration_turn(inbox_event_id)
  WHERE inbox_event_id IS NOT NULL;

CREATE INDEX idx_collaboration_turn_session_status
  ON collaboration_turn(session_id, grant_status, turn_index);
