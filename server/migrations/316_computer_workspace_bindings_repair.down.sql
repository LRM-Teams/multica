-- 316 is a forward-only repair for schema originally owned by migration 307.
-- Rolling back this repair must not drop tables that a healthy 307 already
-- created, so the down migration deliberately leaves the schema intact.
SELECT 1;
