-- The scoped target-agent FK starts with workspace_id, so the legacy
-- target_agent_id-only index cannot support its delete lookup. Creation runs
-- in the idempotent pre-migration hook because CONCURRENTLY must be top-level.
SELECT 1;
