-- Index creation runs in the idempotent pre-migration hook because
-- CREATE INDEX CONCURRENTLY must be issued as a top-level statement.
SELECT 1;
