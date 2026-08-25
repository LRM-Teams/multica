-- Server-authoritative recall lifecycle ledger (spec §3/§4/§14, brief D1/D27/D28).
-- One row per recall plus child trajectories, expansion batches, distinct-view
-- events, immutable submissions, and version-pin retention leases. Identity
-- triggers enforce tenant/graph-kind consistency at the storage layer so
-- cross-tenant or foreign-kind references cannot be persisted even when
-- application validation is bypassed (spec §16).

CREATE TABLE IF NOT EXISTS graph_memory_recall (
  id             uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id   uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  task_id        uuid        NOT NULL REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
  daemon_id      text        NOT NULL,
  runtime_id     uuid        REFERENCES agent_runtime(id) ON DELETE SET NULL,
  graph_kind     text        NOT NULL CHECK (graph_kind IN ('project', 'channel')),
  graph_owner_id uuid        NOT NULL,
  graph_version  integer     NOT NULL CHECK (graph_version >= 1),
  status         text        NOT NULL DEFAULT 'accepted'
    CHECK (status IN ('accepted', 'exploring', 'explore_terminal', 'dive_queued', 'diving', 'completed', 'judge_failed', 'failed')),
  training_mode  text        NOT NULL DEFAULT 'offline_capture'
    CHECK (training_mode IN ('offline_capture', 'online_rl', 'offline_rl')),
  k              integer     NOT NULL CHECK (k BETWEEN 1 AND 64),
  query          text        NOT NULL,
  trace_id       text        NOT NULL,
  schema_version integer     NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  terminal_at    timestamptz,
  UNIQUE (workspace_id, trace_id)
);
CREATE INDEX IF NOT EXISTS graph_memory_recall_ws_created
  ON graph_memory_recall (workspace_id, created_at DESC);

CREATE TABLE IF NOT EXISTS graph_memory_trajectory (
  id                 uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  recall_id          uuid        NOT NULL REFERENCES graph_memory_recall(id) ON DELETE CASCADE,
  workspace_id       uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  seed_index         integer     NOT NULL CHECK (seed_index >= 0),
  status             text        NOT NULL DEFAULT 'running'
    CHECK (status IN ('running', 'found', 'miss', 'error', 'budget', 'timeout')),
  error_kind         text        NOT NULL DEFAULT '',
  summary            text        NOT NULL DEFAULT '',
  viewed_node_ids    jsonb       NOT NULL DEFAULT '[]'::jsonb,
  submitted_node_ids jsonb,
  rounds             integer     NOT NULL DEFAULT 0 CHECK (rounds >= 0),
  model              text        NOT NULL DEFAULT '',
  runtime_meta       jsonb       NOT NULL DEFAULT '{}'::jsonb,
  artifact_ref       text        NOT NULL DEFAULT '',
  schema_version     integer     NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  terminal_at        timestamptz,
  UNIQUE (recall_id, seed_index)
);
CREATE INDEX IF NOT EXISTS graph_memory_trajectory_recall
  ON graph_memory_trajectory (recall_id);

-- One batch per served /expand (round 0 = hybrid-retrieval seeds). request_key
-- is the per-trajectory idempotency key: an identical replay returns the
-- original batch without consuming another round (spec §14).
CREATE TABLE IF NOT EXISTS graph_memory_expansion_batch (
  id             uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  trajectory_id  uuid        NOT NULL REFERENCES graph_memory_trajectory(id) ON DELETE CASCADE,
  round          integer     NOT NULL CHECK (round >= 0),
  anchor_node_id text        NOT NULL DEFAULT '',
  candidate_ids  jsonb       NOT NULL DEFAULT '[]'::jsonb,
  request_key    text        NOT NULL,
  view_quota     integer     NOT NULL CHECK (view_quota >= 1),
  created_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (trajectory_id, round),
  UNIQUE (trajectory_id, request_key)
);

-- Distinct successful views per batch; the primary key IS the distinct-view
-- accounting (spec §4). seq gives the trajectory-level observed order.
CREATE TABLE IF NOT EXISTS graph_memory_view_event (
  seq             bigint       NOT NULL GENERATED ALWAYS AS IDENTITY,
  batch_id        uuid         NOT NULL REFERENCES graph_memory_expansion_batch(id) ON DELETE CASCADE,
  trajectory_id   uuid         NOT NULL REFERENCES graph_memory_trajectory(id) ON DELETE CASCADE,
  node_id         text         NOT NULL,
  first_viewed_at timestamptz  NOT NULL DEFAULT now(),
  PRIMARY KEY (batch_id, node_id)
);
CREATE INDEX IF NOT EXISTS graph_memory_view_event_trajectory_seq
  ON graph_memory_view_event (trajectory_id, seq);

-- Exactly one immutable submission per trajectory (spec §14). payload_hash
-- distinguishes an identical idempotent replay from a conflicting one.
CREATE TABLE IF NOT EXISTS graph_memory_submission (
  trajectory_id uuid        NOT NULL PRIMARY KEY REFERENCES graph_memory_trajectory(id) ON DELETE CASCADE,
  found         boolean     NOT NULL,
  summary       text        NOT NULL,
  node_ids      jsonb       NOT NULL,
  payload_hash  text        NOT NULL,
  submitted_at  timestamptz NOT NULL DEFAULT now()
);

-- Graph-version retention lease: GC/source retirement must not collect a
-- version while any active lease references it (spec §15).
CREATE TABLE IF NOT EXISTS graph_memory_version_lease (
  id             uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id   uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  graph_kind     text        NOT NULL CHECK (graph_kind IN ('project', 'channel')),
  graph_owner_id uuid        NOT NULL,
  graph_version  integer     NOT NULL CHECK (graph_version >= 1),
  consumer_kind  text        NOT NULL CHECK (consumer_kind IN ('recall', 'dive', 'export', 'backtest')),
  consumer_id    uuid        NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  released_at    timestamptz
);
CREATE INDEX IF NOT EXISTS graph_memory_version_lease_open
  ON graph_memory_version_lease (graph_kind, graph_owner_id, graph_version) WHERE released_at IS NULL;

-- Identity consistency (spec §16): the task must belong to the recall's
-- workspace, the runtime (when set) must belong to the same workspace and the
-- invoking daemon, and the graph owner must exist in the table matching
-- graph_kind within the same workspace.
CREATE OR REPLACE FUNCTION graph_memory_recall_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws     uuid;
  v_daemon text;
BEGIN
  SELECT workspace_id INTO v_ws FROM agent_inbox_event WHERE id = NEW.task_id;
  IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_recall: task % is not in workspace %', NEW.task_id, NEW.workspace_id;
  END IF;
  IF NEW.runtime_id IS NOT NULL THEN
    SELECT workspace_id, daemon_id INTO v_ws, v_daemon FROM agent_runtime WHERE id = NEW.runtime_id;
    IF v_ws IS NULL OR v_ws <> NEW.workspace_id OR v_daemon IS DISTINCT FROM NEW.daemon_id THEN
      RAISE EXCEPTION 'graph_memory_recall: runtime % does not match workspace/daemon', NEW.runtime_id;
    END IF;
  END IF;
  IF NEW.graph_kind = 'project' THEN
    SELECT workspace_id INTO v_ws FROM project WHERE id = NEW.graph_owner_id;
  ELSE
    SELECT workspace_id INTO v_ws FROM channel WHERE id = NEW.graph_owner_id;
  END IF;
  IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_recall: % owner % is not in workspace %', NEW.graph_kind, NEW.graph_owner_id, NEW.workspace_id;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_recall_identity
  BEFORE INSERT OR UPDATE ON graph_memory_recall
  FOR EACH ROW EXECUTE FUNCTION graph_memory_recall_validate_identity();

-- A trajectory's tenant must match its recall's tenant.
CREATE OR REPLACE FUNCTION graph_memory_trajectory_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws uuid;
BEGIN
  SELECT workspace_id INTO v_ws FROM graph_memory_recall WHERE id = NEW.recall_id;
  IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_trajectory: recall % is not in workspace %', NEW.recall_id, NEW.workspace_id;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_trajectory_identity
  BEFORE INSERT OR UPDATE ON graph_memory_trajectory
  FOR EACH ROW EXECUTE FUNCTION graph_memory_trajectory_validate_identity();

-- A view event's batch must belong to the same trajectory.
CREATE OR REPLACE FUNCTION graph_memory_view_event_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_trajectory uuid;
BEGIN
  SELECT trajectory_id INTO v_trajectory FROM graph_memory_expansion_batch WHERE id = NEW.batch_id;
  IF v_trajectory IS NULL OR v_trajectory <> NEW.trajectory_id THEN
    RAISE EXCEPTION 'graph_memory_view_event: batch % is not part of trajectory %', NEW.batch_id, NEW.trajectory_id;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_view_event_identity
  BEFORE INSERT OR UPDATE ON graph_memory_view_event
  FOR EACH ROW EXECUTE FUNCTION graph_memory_view_event_validate_identity();

-- Submitted node ids must be unique and a subset of the trajectory's
-- successful views (spec §14). The application commits the final
-- viewed_node_ids and the submission in one transaction.
CREATE OR REPLACE FUNCTION graph_memory_submission_validate() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_viewed jsonb;
  v_id     text;
  v_seen   text[] := '{}';
BEGIN
  SELECT viewed_node_ids INTO v_viewed FROM graph_memory_trajectory WHERE id = NEW.trajectory_id;
  IF v_viewed IS NULL THEN
    RAISE EXCEPTION 'graph_memory_submission: unknown trajectory %', NEW.trajectory_id;
  END IF;
  FOR v_id IN SELECT jsonb_array_elements_text(NEW.node_ids) LOOP
    IF v_seen @> ARRAY[v_id] THEN
      RAISE EXCEPTION 'graph_memory_submission: duplicate node id %', v_id;
    END IF;
    v_seen := v_seen || v_id;
    IF NOT (v_viewed @> to_jsonb(v_id)) THEN
      RAISE EXCEPTION 'graph_memory_submission: node % was never viewed', v_id;
    END IF;
  END LOOP;
  RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_submission_shape
  BEFORE INSERT ON graph_memory_submission
  FOR EACH ROW EXECUTE FUNCTION graph_memory_submission_validate();
