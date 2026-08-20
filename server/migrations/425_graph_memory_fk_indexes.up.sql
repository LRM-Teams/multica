-- Index creation runs in the idempotent pre-migration hook because each
-- CREATE INDEX CONCURRENTLY must be issued as its own top-level statement.
-- The marker is recorded only after every graph-memory cascade FK index is
-- valid and ready.
SELECT 1;
