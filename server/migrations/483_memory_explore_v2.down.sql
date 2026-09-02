-- Task 10 down: drop the Explore plan ledger. The phase-gate table
-- (migration 467) is owned by Task 8A and stays.

BEGIN;

DROP TABLE IF EXISTS memory_explore_plan;

COMMIT;
