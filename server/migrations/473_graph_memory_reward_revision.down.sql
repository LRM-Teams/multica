-- Reverse migration 473: restore the trajectory-unique mutable reward shape.
-- Reward records written under 473 are dropped with the ledger (schema
-- reversal); pre-473 judge_failed rows regain the historical synthetic 0 so
-- the old queries and outbox invariants hold again.

BEGIN;

ALTER TABLE graph_memory_reward_outbox
  DROP CONSTRAINT graph_memory_reward_outbox_delivery_identity;
ALTER TABLE graph_memory_reward_outbox
  ADD CONSTRAINT graph_memory_reward_outbox_trajectory_id_key UNIQUE (trajectory_id);
ALTER TABLE graph_memory_reward_outbox
  DROP COLUMN reward_kind,
  DROP COLUMN reward_revision;

DROP TRIGGER IF EXISTS graph_memory_reward_record_identity ON graph_memory_reward_record;
DROP FUNCTION IF EXISTS graph_memory_reward_record_validate_identity();
DROP TRIGGER IF EXISTS graph_memory_reward_record_write_once ON graph_memory_reward_record;
DROP FUNCTION IF EXISTS protect_graph_memory_reward_record();
DROP TABLE graph_memory_reward_record;

ALTER TABLE graph_memory_trajectory
  DROP CONSTRAINT IF EXISTS graph_memory_trajectory_reward_value_status;

-- Restore the pre-473 judge_failed convention (reward 0) before dropping the
-- classification columns.
UPDATE graph_memory_trajectory SET reward = 0
  WHERE reward_status = 'unavailable' AND reward IS NULL AND dive_status = 'judge_failed';

ALTER TABLE graph_memory_trajectory
  DROP COLUMN reward_status,
  DROP COLUMN reward_revision;

COMMIT;