-- Sticky provider-quota lock (tasks #64 / #77).
-- Per-agent: one runtime hosts many agents; a quota lock on one must not
-- paint siblings. Read-time TTL (provider_blocked_until > now()) — no sweeper.
-- Cleared automatically when until elapses; writing a new lock extends/replaces.
ALTER TABLE agent
  ADD COLUMN provider_blocked_until TIMESTAMPTZ,
  ADD COLUMN provider_block_reason TEXT NOT NULL DEFAULT '',
  ADD COLUMN provider_block_detail TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN agent.provider_blocked_until IS
  'When set and in the future, agent cannot take work (provider quota lock). Heartbeats must not clear this.';
COMMENT ON COLUMN agent.provider_block_reason IS
  'taskfailure reason, e.g. agent_error.provider_quota_limit';
COMMENT ON COLUMN agent.provider_block_detail IS
  'User-facing error snippet including reset-until text when present';
