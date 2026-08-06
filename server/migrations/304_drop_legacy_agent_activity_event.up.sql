-- #2424 hard cut: Runner snapshots and entries are the sole Activity store.
-- Old timeline history is intentionally discarded and is not backfilled.
DROP TABLE IF EXISTS agent_activity_event;
