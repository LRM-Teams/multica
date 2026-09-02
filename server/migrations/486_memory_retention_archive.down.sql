-- Task 17 down: drop the retention/archive objects. The bootstrap policy
-- rows inserted by the up migration are removed with their tables; the
-- hot blobs retired by archiving are NOT resurrected (their bytes were
-- retired, not deleted, and down never fabricates content).
DROP TABLE IF EXISTS memory_retention_sweep_cursor;
DROP TABLE IF EXISTS memory_archive_restore_lease;
DROP TABLE IF EXISTS memory_archive_manifest;
DROP TABLE IF EXISTS memory_retention_policy;
