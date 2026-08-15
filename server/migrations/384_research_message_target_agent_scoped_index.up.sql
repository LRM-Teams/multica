-- The pre-migration hook creates the supporting composite index concurrently.
-- This marker makes the repair run for databases that already applied the
-- earlier Agent FK index hooks before the scoped Research Message FK existed.
SELECT 1;
