-- Online RL sessions and durable reward outbox for graph-memory Explore
-- trajectories (spec §6/§7, brief D13; acceptance A12/A25/A29).
--
-- graph_memory_rl_session holds one row per trajectory of an online_rl
-- recall: the durable mapping from trajectory to AReaL session id and proxy
-- key. The row is written as an opening intent BEFORE the StartSession RPC
-- and fenced by a generation counter, so a crash mid-open can be reconciled
-- without duplicate effective sessions (the stale-session reaper owns
-- eventual AReaL-side cleanup; nothing is exported or removed inline).
-- proxy_key is cleared only after the reward's durable terminal ack
-- (graph_memory_reward_outbox.status = 'delivered'), never before.
--
-- Proxy keys live only in this table and in-memory delivery paths. They must
-- never appear in logs, API responses, artifacts, error messages, metrics,
-- model context, or process arguments (A29).

CREATE TABLE IF NOT EXISTS graph_memory_rl_session (
  id             uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id   uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  trajectory_id  uuid        NOT NULL UNIQUE REFERENCES graph_memory_trajectory(id) ON DELETE CASCADE,
  recall_id      uuid        NOT NULL REFERENCES graph_memory_recall(id) ON DELETE CASCADE,
  status         text        NOT NULL DEFAULT 'opening'
    CHECK (status IN ('opening', 'open', 'rewarded', 'closed', 'failed')),
  generation     integer     NOT NULL DEFAULT 1 CHECK (generation >= 1),
  session_id     text        NOT NULL DEFAULT '',
  proxy_key      text        NOT NULL DEFAULT '',
  last_error     text        NOT NULL DEFAULT '',
  schema_version integer     NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  opened_at      timestamptz,
  key_cleared_at timestamptz,
  closed_at      timestamptz
);
-- Reaper candidates: terminal-cleanup scans by status and age.
CREATE INDEX IF NOT EXISTS graph_memory_rl_session_reap
  ON graph_memory_rl_session (status, updated_at);

CREATE TABLE IF NOT EXISTS graph_memory_reward_outbox (
  id              uuid             NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    uuid             NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  trajectory_id   uuid             NOT NULL UNIQUE REFERENCES graph_memory_trajectory(id) ON DELETE CASCADE,
  reward          double precision NOT NULL,
  status          text             NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'delivering', 'delivered', 'failed')),
  attempts        integer          NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts    integer          NOT NULL DEFAULT 8 CHECK (max_attempts >= 1),
  next_attempt_at timestamptz      NOT NULL DEFAULT now(),
  last_error      text             NOT NULL DEFAULT '',
  schema_version  integer          NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
  created_at      timestamptz      NOT NULL DEFAULT now(),
  updated_at      timestamptz      NOT NULL DEFAULT now(),
  delivered_at    timestamptz
);
-- Claim candidates: due pending rows first, then stale in-flight deliveries.
CREATE INDEX IF NOT EXISTS graph_memory_reward_outbox_claimable
  ON graph_memory_reward_outbox (status, next_attempt_at, created_at);

-- A session row must mirror its trajectory's tenant and recall exactly: the
-- durable session mapping can never drift across tenants or recalls even when
-- application validation is bypassed (spec §16).
CREATE OR REPLACE FUNCTION graph_memory_rl_session_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws     uuid;
  v_recall uuid;
BEGIN
  SELECT workspace_id, recall_id INTO v_ws, v_recall
    FROM graph_memory_trajectory WHERE id = NEW.trajectory_id;
  IF v_ws IS NULL THEN
    RAISE EXCEPTION 'graph_memory_rl_session: trajectory % does not exist', NEW.trajectory_id;
  END IF;
  IF v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_rl_session: trajectory % is not in workspace %', NEW.trajectory_id, NEW.workspace_id;
  END IF;
  IF v_recall <> NEW.recall_id THEN
    RAISE EXCEPTION 'graph_memory_rl_session: trajectory % does not belong to recall %', NEW.trajectory_id, NEW.recall_id;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER graph_memory_rl_session_identity
  BEFORE INSERT OR UPDATE ON graph_memory_rl_session
  FOR EACH ROW EXECUTE FUNCTION graph_memory_rl_session_validate_identity();

-- An outbox row must live in its trajectory's tenant.
CREATE OR REPLACE FUNCTION graph_memory_reward_outbox_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws uuid;
BEGIN
  SELECT workspace_id INTO v_ws
    FROM graph_memory_trajectory WHERE id = NEW.trajectory_id;
  IF v_ws IS NULL THEN
    RAISE EXCEPTION 'graph_memory_reward_outbox: trajectory % does not exist', NEW.trajectory_id;
  END IF;
  IF v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_reward_outbox: trajectory % is not in workspace %', NEW.trajectory_id, NEW.workspace_id;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER graph_memory_reward_outbox_identity
  BEFORE INSERT OR UPDATE ON graph_memory_reward_outbox
  FOR EACH ROW EXECUTE FUNCTION graph_memory_reward_outbox_validate_identity();
