-- Idle resident provider crash fact (task #42② / Parker Raft status ②).
-- Per-agent (not per-runtime): one machine/runtime can host many agents with
-- independent provider processes; a crash on one must not paint the others.
ALTER TABLE agent ADD COLUMN crashed_since TIMESTAMPTZ;
