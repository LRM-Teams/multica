-- Immutable reward revisions and delivery identity (spec 14.2/14.4, Task 19;
-- acceptance A46/A48). The mutable trajectory-unique reward becomes:
--   * an immutable per-revision ledger (graph_memory_reward_record) —
--     re-evaluation appends revision N+1, never overwrites, and a replay of
--     the same identity must carry the same value;
--   * a trajectory projection (reward_status / reward_revision) that
--     training selection and offline export can exclude on;
--   * an outbox keyed by (trajectory_id, reward_kind, reward_revision).
-- Judge infrastructure failure is reward_status='unavailable' with a NULL
-- value — never a numeric 0 (A46) — and unavailable rewards never enter the
-- outbox.

BEGIN;

-- Trajectory projection of the latest reward revision.
--   ''            not yet judged
--   graded        model-graded numeric reward (spec 14.2 formula)
--   deterministic deterministic negative for the explore agent's own
--                 budget/violation terminal states
--   unavailable   judge infrastructure failure — no numeric value, never
--                 trained or delivered (A46)
--   rejected      judge refused the input (scope/invalid input)
ALTER TABLE graph_memory_trajectory
  ADD COLUMN reward_status   text NOT NULL DEFAULT ''
    CHECK (reward_status IN ('', 'graded', 'deterministic', 'unavailable', 'rejected')),
  ADD COLUMN reward_revision integer NOT NULL DEFAULT 0
    CHECK (reward_revision >= 0);

-- Normalize pre-473 rows to the new classification. Values recorded under
-- the old semantics stay as recorded (bypassed keeps its historical 0);
-- judge_failed rows lose their old synthetic reward 0 — that number was
-- never a real grade (A46). No ledger rows are backfilled: the revision
-- ledger starts at migration 473 and the judged input manifests of history
-- cannot be reconstructed honestly.
UPDATE graph_memory_trajectory SET reward_status = 'graded', reward_revision = 1
  WHERE dive_status = 'graded';
UPDATE graph_memory_trajectory SET reward_status = 'deterministic', reward_revision = 1
  WHERE dive_status = 'bypassed';
UPDATE graph_memory_trajectory SET reward_status = 'unavailable', reward_revision = 1, reward = NULL
  WHERE dive_status = 'judge_failed';

-- Coherence invariants (database-enforced; application validation is never
-- the only guard, spec 16):
--   graded/deterministic classifications carry a numeric value;
--   unavailable/rejected classifications never do.
ALTER TABLE graph_memory_trajectory
  ADD CONSTRAINT graph_memory_trajectory_reward_value_status CHECK (
    (reward_status NOT IN ('graded', 'deterministic') OR reward IS NOT NULL)
    AND
    (reward_status NOT IN ('unavailable', 'rejected') OR reward IS NULL)
  );

-- The immutable reward ledger: one row per (trajectory, reward_kind,
-- revision). Rows are write-once — UPDATE is forbidden (a re-evaluation
-- appends the next revision; a consumed record is never overwritten, A48).
-- DELETE stays legal so tenant erasure cascades through the trajectory FK.
CREATE TABLE graph_memory_reward_record (
  id                  uuid            NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id        uuid            NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  trajectory_id       uuid            NOT NULL REFERENCES graph_memory_trajectory(id) ON DELETE CASCADE,
  reward_kind         text            NOT NULL DEFAULT 'explore'
    CHECK (reward_kind IN ('explore')),
  revision            integer         NOT NULL CHECK (revision >= 1),
  status              text            NOT NULL
    CHECK (status IN ('available', 'unavailable', 'rejected')),
  value               double precision,
  components          jsonb           NOT NULL DEFAULT '{}'::jsonb,
  policy_version      text            NOT NULL,
  input_manifest_hash text            NOT NULL,
  created_at          timestamptz     NOT NULL DEFAULT now(),
  UNIQUE (trajectory_id, reward_kind, revision),
  CHECK (status <> 'available' OR value IS NOT NULL),
  CHECK (status = 'available' OR value IS NULL)
);

CREATE INDEX graph_memory_reward_record_trajectory
  ON graph_memory_reward_record (trajectory_id, reward_kind, revision);

-- Write-once: revisions are appended, never edited. UPDATE is forbidden;
-- DELETE serves only the tenant-erasure cascade.
CREATE OR REPLACE FUNCTION protect_graph_memory_reward_record()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'graph memory reward records are immutable: UPDATE is forbidden, append a new revision instead';
END;
$$;

CREATE TRIGGER graph_memory_reward_record_write_once
  BEFORE UPDATE ON graph_memory_reward_record
  FOR EACH ROW EXECUTE FUNCTION protect_graph_memory_reward_record();

-- A record must live in its trajectory's tenant (spec 16).
CREATE OR REPLACE FUNCTION graph_memory_reward_record_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws uuid;
BEGIN
  SELECT workspace_id INTO v_ws
    FROM graph_memory_trajectory WHERE id = NEW.trajectory_id;
  IF v_ws IS NULL THEN
    RAISE EXCEPTION 'graph_memory_reward_record: trajectory % does not exist', NEW.trajectory_id;
  END IF;
  IF v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_reward_record: trajectory % is not in workspace %', NEW.trajectory_id, NEW.workspace_id;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER graph_memory_reward_record_identity
  BEFORE INSERT ON graph_memory_reward_record
  FOR EACH ROW EXECUTE FUNCTION graph_memory_reward_record_validate_identity();

-- Delivery identity: the outbox keys rewards by (trajectory, kind, revision)
-- so a re-evaluation delivers its own revision exactly once (Task 19 Step 5).
ALTER TABLE graph_memory_reward_outbox
  ADD COLUMN reward_kind     text    NOT NULL DEFAULT 'explore'
    CHECK (reward_kind IN ('explore')),
  ADD COLUMN reward_revision integer NOT NULL DEFAULT 1
    CHECK (reward_revision >= 1);

ALTER TABLE graph_memory_reward_outbox
  DROP CONSTRAINT graph_memory_reward_outbox_trajectory_id_key;
ALTER TABLE graph_memory_reward_outbox
  ADD CONSTRAINT graph_memory_reward_outbox_delivery_identity
  UNIQUE (trajectory_id, reward_kind, reward_revision);

COMMIT;