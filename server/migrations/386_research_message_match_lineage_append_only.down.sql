-- The append-only repair is intentionally retained on downgrade: restoring the
-- migration-373 delete/rebuild behavior would violate frozen-history guards.
SELECT 1;
