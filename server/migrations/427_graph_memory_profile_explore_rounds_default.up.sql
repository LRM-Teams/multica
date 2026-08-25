-- The merged /explore protocol counts one round per served node, so the
-- default budget is larger than the legacy /view+/expand round count
-- (matches daemon DefaultGraphExploreMaxRounds). Existing rows holding the
-- old protocol default of 3 move to 6 as well: 3 was the pre-merge insert
-- default, and an explicit admin override to exactly 3 cannot be
-- distinguished from it, so the protocol bump wins for both.
ALTER TABLE graph_memory_profile ALTER COLUMN explore_max_rounds SET DEFAULT 6;
UPDATE graph_memory_profile SET explore_max_rounds = 6 WHERE explore_max_rounds = 3;
