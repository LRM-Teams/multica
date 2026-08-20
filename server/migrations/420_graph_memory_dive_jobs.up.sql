-- Durable Dive judge job queue (spec §5/§6, brief D6/D8; acceptance A8/A25).
-- One job per recall, enqueued only after all K explore trajectories reach a
-- terminal state (the K barrier). Lease/attempt fencing lets a crashed
-- worker's job be re-leased without duplicating external effects; bounded
-- retries end in a terminal failure that moves the recall to judge_failed
-- with reward 0 for normally completed runs and no authoritative ground
-- truth.

CREATE TABLE IF NOT EXISTS graph_memory_dive_job (
  id               uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  recall_id        uuid        NOT NULL UNIQUE REFERENCES graph_memory_recall(id) ON DELETE CASCADE,
  workspace_id     uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  trace_id         text        NOT NULL,
  graph_kind       text        NOT NULL CHECK (graph_kind IN ('project', 'channel')),
  graph_owner_id   uuid        NOT NULL,
  graph_version    integer     NOT NULL CHECK (graph_version >= 1),
  status           text        NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'running', 'completed', 'failed')),
  attempts         integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts     integer     NOT NULL DEFAULT 4 CHECK (max_attempts >= 1),
  leased_by        text        NOT NULL DEFAULT '',
  lease_expires_at timestamptz,
  incomplete       boolean     NOT NULL DEFAULT false,
  error_kind       text        NOT NULL DEFAULT '',
  last_error       text        NOT NULL DEFAULT '',
  model            text        NOT NULL DEFAULT '',
  result           jsonb       NOT NULL DEFAULT '{}'::jsonb,
  schema_version   integer     NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  terminal_at      timestamptz,
  UNIQUE (workspace_id, trace_id)
);
-- Lease candidates: queued jobs first, then expired leases, oldest first.
CREATE INDEX IF NOT EXISTS graph_memory_dive_job_leasable
  ON graph_memory_dive_job (status, lease_expires_at, created_at);

-- Per-trajectory Dive grading (spec §7): continuous scores in [0,1], the
-- min-dimension overall, and the unclamped reward are stored with the
-- trajectory for offline export. dive_status '' means not yet graded;
-- 'bypassed' covers Explore error/timeout/budget runs (reward 0, no model
-- grading); 'judge_failed' covers terminal Dive infrastructure failure
-- (reward 0 for otherwise normal runs, no authoritative ground truth).
ALTER TABLE graph_memory_trajectory
  ADD COLUMN dive_status        text             NOT NULL DEFAULT ''
    CHECK (dive_status IN ('', 'graded', 'bypassed', 'judge_failed')),
  ADD COLUMN score_relevance    double precision CHECK (score_relevance BETWEEN 0 AND 1),
  ADD COLUMN score_groundedness double precision CHECK (score_groundedness BETWEEN 0 AND 1),
  ADD COLUMN score_completeness double precision CHECK (score_completeness BETWEEN 0 AND 1),
  ADD COLUMN overall_score      double precision CHECK (overall_score BETWEEN 0 AND 1),
  ADD COLUMN reward             double precision;

-- A dive job must mirror its recall's identity and pinned version exactly:
-- the job can never drift to another tenant, graph, or version than the
-- recall it judges (spec §16).
CREATE OR REPLACE FUNCTION graph_memory_dive_job_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws      uuid;
  v_kind    text;
  v_owner   uuid;
  v_version integer;
  v_trace   text;
BEGIN
  SELECT workspace_id, graph_kind, graph_owner_id, graph_version, trace_id
    INTO v_ws, v_kind, v_owner, v_version, v_trace
  FROM graph_memory_recall WHERE id = NEW.recall_id;
  IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_dive_job: recall % is not in workspace %', NEW.recall_id, NEW.workspace_id;
  END IF;
  IF v_kind <> NEW.graph_kind OR v_owner <> NEW.graph_owner_id THEN
    RAISE EXCEPTION 'graph_memory_dive_job: graph identity does not match recall %', NEW.recall_id;
  END IF;
  IF v_version <> NEW.graph_version THEN
    RAISE EXCEPTION 'graph_memory_dive_job: pinned version % does not match recall %', NEW.graph_version, NEW.recall_id;
  END IF;
  IF v_trace <> NEW.trace_id THEN
    RAISE EXCEPTION 'graph_memory_dive_job: trace id does not match recall %', NEW.recall_id;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_dive_job_identity
  BEFORE INSERT OR UPDATE ON graph_memory_dive_job
  FOR EACH ROW EXECUTE FUNCTION graph_memory_dive_job_validate_identity();
