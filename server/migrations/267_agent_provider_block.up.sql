-- Sticky provider-quota lock display fact (tasks #64 / #77).
-- Per-agent: one runtime hosts many agents; a quota lock on one must not
-- paint siblings. Heartbeats must not clear this.
--
-- Lock semantics (Parker 2026-08-02):
--   locked  <=> provider_block_detail <> ''
--              AND (provider_blocked_until IS NULL  -- unknown end, still locked
--                   OR provider_blocked_until > now())
--   unlocked <=> detail empty, OR until is known and has elapsed
-- Never invent a reset timestamp when parse fails (#815: unknown over invented).
ALTER TABLE agent
  ADD COLUMN provider_blocked_until TIMESTAMPTZ,
  ADD COLUMN provider_block_detail TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN agent.provider_blocked_until IS
  'Known provider-quota reset time. NULL while locked means unknown end (still locked).';
COMMENT ON COLUMN agent.provider_block_detail IS
  'Non-empty means provider-quota locked; holds user-facing error snippet.';
