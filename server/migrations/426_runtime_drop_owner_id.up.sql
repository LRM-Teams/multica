-- LRM-1570: owner lives only at the machine (Computer) level.
--
-- Before this migration, agent_runtime carried its own owner_id column
-- (added in 032) that was written at daemon register time. The UI read
-- `machine.runtimes[0].owner_id` from it, which was NULL for machines whose
-- runtime rows predated the column or whose binding owner never made it into
-- the runtime row — producing the "no owner" display bug (b413e86e /
-- 743b1ca6 / 9a740b51).
--
-- Frank's direction (1465/1466, thread 7ad33e9a): runtimes do not own
-- anything; ownership is a property of the Computer entity. The authoritative
-- owner lives in the computers table (per id) and computer_workspace_bindings
-- (per id + workspace_id). This migration:
--   1. renames computer_identity_owner -> computers (the entity itself, with
--      owner_id as a column on it — not a separate owner table),
--   2. renames its primary key column daemon_id -> id to align with the
--      entity-table id convention used by user / workspace / member /
--      agent_runtime,
--   3. drops agent_runtime.owner_id entirely, so the invariant becomes
--      "owner exists exactly once, at the Computer layer".
--
-- Note: the key stays TEXT (it is the externally-issued daemon UUID written
-- once, not an auto-minted gen_random_uuid()), so `id` here is the natural
-- key value, analogous to machine_upgrade.id which is also TEXT PRIMARY KEY.
--
-- Forward-only: code that still reads agent_runtime.owner_id must ship in the
-- same release (see handler/sql changes in LRM-1570).

ALTER TABLE computer_identity_owner RENAME TO computers;
ALTER TABLE computers RENAME COLUMN daemon_id TO id;

ALTER TABLE agent_runtime DROP COLUMN IF EXISTS owner_id;