-- Graph Memory Agent mode: workspace defaults, channel overrides, managed
-- identities, durable execution state, steering ledger, and citations.
ALTER TABLE graph_memory_profile
  ADD COLUMN graph_memory_mode text NOT NULL DEFAULT 'agent'
    CHECK (graph_memory_mode IN ('inject', 'agent')),
  ADD COLUMN memory_agent_runtime_id uuid REFERENCES agent_runtime(id) ON DELETE SET NULL,
  ADD COLUMN memory_agent_model text NOT NULL DEFAULT '',
  ADD COLUMN memory_agent_thinking text NOT NULL DEFAULT '',
  ADD COLUMN recall_ttt_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN consolidation_ttt_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN memory_agent_idle_grace_seconds integer NOT NULL DEFAULT 120
    CHECK (memory_agent_idle_grace_seconds BETWEEN 30 AND 3600),
  ADD COLUMN memory_agent_max_nodes_per_call integer NOT NULL DEFAULT 4
    CHECK (memory_agent_max_nodes_per_call BETWEEN 1 AND 16),
  ADD COLUMN memory_agent_max_nodes_per_minute integer NOT NULL DEFAULT 30
    CHECK (memory_agent_max_nodes_per_minute BETWEEN 1 AND 600),
  ADD COLUMN memory_agent_max_continuous_turn_seconds integer NOT NULL DEFAULT 600
    CHECK (memory_agent_max_continuous_turn_seconds BETWEEN 30 AND 3600),
  ADD COLUMN memory_agent_max_tokens_per_hour bigint NOT NULL DEFAULT 200000
    CHECK (memory_agent_max_tokens_per_hour BETWEEN 1000 AND 10000000);

UPDATE graph_memory_profile
SET recall_ttt_enabled = ttt_enabled,
    consolidation_ttt_enabled = ttt_enabled;

ALTER TABLE channel
  ADD COLUMN graph_memory_mode_override text NOT NULL DEFAULT 'inherit'
    CHECK (graph_memory_mode_override IN ('inherit', 'inject', 'agent'));

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_managed_role_check;
ALTER TABLE agent ADD CONSTRAINT agent_managed_role_check
  CHECK (managed_role IS NULL OR managed_role IN ('research_fleet', 'graph_memory_channel'));

CREATE TABLE graph_memory_channel_agent (
  channel_id uuid PRIMARY KEY REFERENCES channel(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id uuid UNIQUE REFERENCES agent(id) ON DELETE SET NULL,
  runtime_id uuid REFERENCES agent_runtime(id) ON DELETE SET NULL,
  sponsor_user_id uuid REFERENCES "user"(id) ON DELETE SET NULL,
  handle text NOT NULL,
  display_name text NOT NULL,
  status text NOT NULL DEFAULT 'provisioning'
    CHECK (status IN ('provisioning', 'active', 'blocked', 'inactive')),
  blocked_reason text NOT NULL DEFAULT '',
  delegated_credential_version bigint NOT NULL DEFAULT 1,
  last_notified_status text NOT NULL DEFAULT '',
  config_version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, handle)
);

CREATE INDEX graph_memory_channel_agent_workspace_status_idx
  ON graph_memory_channel_agent(workspace_id, status);

CREATE TABLE graph_memory_agent_state (
  channel_id uuid PRIMARY KEY REFERENCES graph_memory_channel_agent(channel_id) ON DELETE CASCADE,
  consumed_seq bigint NOT NULL DEFAULT 0 CHECK (consumed_seq >= 0),
  graph_version bigint NOT NULL DEFAULT 0 CHECK (graph_version >= 0),
  objective text NOT NULL DEFAULT '',
  observations jsonb NOT NULL DEFAULT '[]'::jsonb,
  rejected_branches jsonb NOT NULL DEFAULT '[]'::jsonb,
  open_questions jsonb NOT NULL DEFAULT '[]'::jsonb,
  candidate_node_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  viewed_node_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  pending_targets jsonb NOT NULL DEFAULT '[]'::jsonb,
  posted_fingerprints jsonb NOT NULL DEFAULT '[]'::jsonb,
  next_hint text NOT NULL DEFAULT '',
  lease_expires_at timestamptz,
  active_run_id uuid,
  state_version bigint NOT NULL DEFAULT 1,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE graph_memory_agent_run (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id uuid NOT NULL REFERENCES graph_memory_channel_agent(channel_id) ON DELETE CASCADE,
  target_kind text NOT NULL DEFAULT 'ambient' CHECK (target_kind IN ('ambient', 'channel', 'thread')),
  target_id uuid,
  status text NOT NULL DEFAULT 'running'
    CHECK (status IN ('running', 'checkpointed', 'submitted', 'failed', 'cancelled', 'state_reset')),
  initial_query text NOT NULL DEFAULT '',
  effective_objective text NOT NULL DEFAULT '',
  graph_kind text NOT NULL DEFAULT '',
  graph_owner_id uuid,
  graph_version bigint NOT NULL DEFAULT 0,
  fencing_token bigint NOT NULL,
  input_tokens bigint NOT NULL DEFAULT 0,
  output_tokens bigint NOT NULL DEFAULT 0,
  cost_micros bigint NOT NULL DEFAULT 0,
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz
);

ALTER TABLE graph_memory_agent_state
  ADD CONSTRAINT graph_memory_agent_state_active_run_id_fkey
  FOREIGN KEY (active_run_id) REFERENCES graph_memory_agent_run(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX graph_memory_agent_run_one_active_idx
  ON graph_memory_agent_run(channel_id) WHERE status = 'running';
CREATE INDEX graph_memory_agent_run_workspace_started_idx
  ON graph_memory_agent_run(workspace_id, started_at DESC);

CREATE TABLE graph_memory_agent_trajectory (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id uuid NOT NULL UNIQUE REFERENCES graph_memory_agent_run(id) ON DELETE CASCADE,
  status text NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'submitted', 'checkpointed', 'failed')),
  viewed_node_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  state_patch jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz
);

CREATE TABLE graph_memory_agent_steering_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id uuid NOT NULL REFERENCES graph_memory_agent_run(id) ON DELETE CASCADE,
  trajectory_id uuid NOT NULL REFERENCES graph_memory_agent_trajectory(id) ON DELETE CASCADE,
  message_id uuid NOT NULL REFERENCES channel_message(id) ON DELETE CASCADE,
  ordinal bigint NOT NULL,
  actor jsonb NOT NULL DEFAULT '{}'::jsonb,
  message jsonb NOT NULL DEFAULT '{}'::jsonb,
  target jsonb NOT NULL DEFAULT '{}'::jsonb,
  accepted_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_id, ordinal),
  UNIQUE (run_id, message_id)
);

CREATE TABLE graph_memory_agent_tool_operation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  trajectory_id uuid NOT NULL REFERENCES graph_memory_agent_trajectory(id) ON DELETE CASCADE,
  idempotency_key text NOT NULL,
  operation text NOT NULL CHECK (operation IN ('start', 'explore', 'redirect', 'submit', 'checkpoint')),
  request jsonb NOT NULL DEFAULT '{}'::jsonb,
  response jsonb NOT NULL DEFAULT '{}'::jsonb,
  error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (trajectory_id, idempotency_key)
);

CREATE TABLE graph_memory_agent_citation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id uuid NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  message_id uuid REFERENCES channel_message(id) ON DELETE SET NULL,
  trajectory_id uuid NOT NULL REFERENCES graph_memory_agent_trajectory(id) ON DELETE RESTRICT,
  node_id text NOT NULL,
  graph_version bigint NOT NULL,
  level text NOT NULL DEFAULT '',
  epistemic_status text NOT NULL DEFAULT '',
  tags jsonb NOT NULL DEFAULT '[]'::jsonb,
  title text NOT NULL DEFAULT '',
  first_paragraph text NOT NULL DEFAULT '',
  excerpt text NOT NULL CHECK (char_length(excerpt) <= 2000),
  content_hash text NOT NULL,
  captured_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (trajectory_id, node_id)
);

CREATE INDEX graph_memory_agent_citation_message_idx
  ON graph_memory_agent_citation(message_id);
