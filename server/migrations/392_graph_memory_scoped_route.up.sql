-- Graph memory scoped routing (spec §4): PostgreSQL is the routing
-- authority for channel graph residency. Route = current write target;
-- lineage = immutable per-generation history.
CREATE TABLE IF NOT EXISTS graph_memory_channel_route (
  workspace_id          uuid        NOT NULL,
  channel_id            uuid        NOT NULL PRIMARY KEY,
  routing_mode          text        NOT NULL CHECK (routing_mode IN ('standalone', 'project_lineage')),
  current_graph_kind    text        NOT NULL CHECK (current_graph_kind IN ('project', 'channel')),
  current_graph_owner_id uuid       NOT NULL,
  generation            bigint      NOT NULL,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS graph_memory_channel_lineage (
  workspace_id   uuid        NOT NULL,
  channel_id     uuid        NOT NULL,
  generation     bigint      NOT NULL,
  graph_kind     text        NOT NULL CHECK (graph_kind IN ('project', 'channel')),
  graph_owner_id uuid        NOT NULL,
  valid_from     timestamptz NOT NULL DEFAULT now(),
  valid_to       timestamptz,
  PRIMARY KEY (channel_id, generation)
);

-- Manual/scheduled consolidation run records (spec §10 governance).
CREATE TABLE IF NOT EXISTS graph_memory_consolidation_run (
  id           uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid        NOT NULL,
  status       text        NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
  trigger_kind text        NOT NULL DEFAULT 'manual',
  error        text        NOT NULL DEFAULT '',
  details      jsonb       NOT NULL DEFAULT '{}'::jsonb,
  created_at   timestamptz NOT NULL DEFAULT now(),
  started_at   timestamptz,
  finished_at  timestamptz
);
CREATE INDEX IF NOT EXISTS graph_memory_consolidation_run_ws_created
  ON graph_memory_consolidation_run (workspace_id, created_at DESC);

-- Readiness gate (spec §10/§13): graph jobs stay inert until the scoped
-- writer acceptance gates pass and an operator sets this flag. timezone is
-- the workspace memory-profile timezone for daily nodes (spec §6); empty
-- falls back to memorycuration.DefaultTimezone (Asia/Shanghai).
ALTER TABLE graph_memory_profile
  ADD COLUMN IF NOT EXISTS scoped_writer_ready boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS timezone text NOT NULL DEFAULT '';
