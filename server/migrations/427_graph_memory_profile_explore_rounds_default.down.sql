-- Restores the legacy default only. Migrated rows stay at 6: after the up
-- ran, an explicit admin 6 is indistinguishable from a migrated one.
ALTER TABLE graph_memory_profile ALTER COLUMN explore_max_rounds SET DEFAULT 3;
