-- LRM-1570 down: reinstate the legacy schema.
--
-- Restores agent_runtime.owner_id (nullable, no backfill — a rollback only
-- reopens the column for an older server image that wrote it) and renames
-- computers back to computer_identity_owner. A rollback does not restore
-- the code that used runtime-level ownership, so runtime owner_id rows will be
-- NULL until a legacy server writes them again — which is acceptable for a
-- rollback path.

ALTER TABLE computers RENAME TO computer_identity_owner;

ALTER TABLE agent_runtime ADD COLUMN IF NOT EXISTS owner_id UUID REFERENCES "user"(id);