-- A producer fact id is idempotent only when its complete Activity envelope is
-- unchanged. Without this fence, a replay using the same sequence/fact could
-- overwrite the replaceable Snapshot while its Entries were deduplicated.
ALTER TABLE agent_activity_launch
    ADD COLUMN last_activity_fingerprint TEXT NOT NULL DEFAULT ''
    CHECK (length(last_activity_fingerprint) <= 64);
