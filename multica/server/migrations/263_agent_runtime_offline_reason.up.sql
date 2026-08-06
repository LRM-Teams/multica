BEGIN;

-- Task ① (align with Raft status work): lets the daemon's own intentional
-- shutdown survive to the read-time display status, so "offline" can
-- eventually split into "will probably reconnect" (network drop, no fact
-- known) vs "was stopped on purpose" (daemon deregistered, sandbox
-- teardown, etc). Nullable, no default: NULL means "we don't know why it's
-- offline" (sweeper's silence-based flip, or a row never explicitly
-- deregistered) — the same honest-unknown default as today. Non-null is a
-- reason_code string, mirroring the vocabulary agent_activity_event already
-- uses (no new enum type).
ALTER TABLE agent_runtime ADD COLUMN offline_reason TEXT;

COMMIT;
