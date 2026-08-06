-- LRM-1004: Goal sub-goals under the channel's unique main Goal.
-- Parallel by default; explicit serial deps; single Responsible; versioned brief.

CREATE TABLE channel_goal_subgoal (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  goal_id UUID NOT NULL REFERENCES channel_goal(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 200),
  purpose TEXT NOT NULL CHECK (length(btrim(purpose)) BETWEEN 1 AND 4000),
  completion_boundary TEXT NOT NULL DEFAULT '' CHECK (length(completion_boundary) <= 4000),
  brief TEXT NOT NULL DEFAULT '' CHECK (length(brief) <= 16000),
  current_conclusion TEXT NOT NULL DEFAULT '' CHECK (length(current_conclusion) <= 16000),
  status TEXT NOT NULL DEFAULT 'captured'
    CHECK (status IN ('captured', 'in_progress', 'waiting', 'resolved', 'cancelled')),
  version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
  responsible_type TEXT NOT NULL CHECK (responsible_type IN ('agent', 'member')),
  responsible_id UUID NOT NULL,
  waiting_on JSONB NOT NULL DEFAULT 'null'::jsonb,
  waiting_on_verified_at TIMESTAMPTZ,
  artifact_refs JSONB NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(artifact_refs) = 'array'),
  activity_delta JSONB NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(activity_delta) = 'array'),
  created_by_type TEXT NOT NULL CHECK (created_by_type IN ('user', 'agent')),
  created_by_id UUID NOT NULL,
  updated_by_type TEXT NOT NULL CHECK (updated_by_type IN ('user', 'agent')),
  updated_by_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ
);

CREATE INDEX channel_goal_subgoal_goal_status
  ON channel_goal_subgoal (goal_id, status, created_at DESC);
CREATE INDEX channel_goal_subgoal_channel_open
  ON channel_goal_subgoal (channel_id, created_at DESC)
  WHERE status IN ('captured', 'in_progress', 'waiting');
CREATE INDEX channel_goal_subgoal_responsible
  ON channel_goal_subgoal (workspace_id, responsible_type, responsible_id);

CREATE TABLE channel_goal_subgoal_participant (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  subgoal_id UUID NOT NULL REFERENCES channel_goal_subgoal(id) ON DELETE CASCADE,
  participant_type TEXT NOT NULL CHECK (participant_type IN ('agent', 'member')),
  participant_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (subgoal_id, participant_type, participant_id)
);

CREATE INDEX channel_goal_subgoal_participant_lookup
  ON channel_goal_subgoal_participant (workspace_id, participant_type, participant_id);

-- Explicit serial dependency: depends_on must be resolved before subgoal can enter in_progress.
CREATE TABLE channel_goal_subgoal_dep (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  subgoal_id UUID NOT NULL REFERENCES channel_goal_subgoal(id) ON DELETE CASCADE,
  depends_on_subgoal_id UUID NOT NULL REFERENCES channel_goal_subgoal(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (subgoal_id, depends_on_subgoal_id),
  CHECK (subgoal_id <> depends_on_subgoal_id)
);

CREATE INDEX channel_goal_subgoal_dep_depends_on
  ON channel_goal_subgoal_dep (depends_on_subgoal_id);
