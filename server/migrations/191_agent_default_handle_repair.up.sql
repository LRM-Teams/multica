-- The deterministic repair runs in cmd/migrate before this record is applied.
-- Keep SQL as a no-op so the migration remains auditable after the hook runs.
SELECT 1;
