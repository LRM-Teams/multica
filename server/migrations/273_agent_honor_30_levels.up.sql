-- Align permanent agent honor progression with the 30 shipped level crests.
UPDATE agent_honor_state
SET level = LEAST(30, GREATEST(1, floor(sqrt(total_xp::numeric / 25))::int + 1)),
    updated_at = now()
WHERE level <> LEAST(30, GREATEST(1, floor(sqrt(total_xp::numeric / 25))::int + 1));

ALTER TABLE agent_honor_state
    ADD CONSTRAINT agent_honor_state_level_max_check CHECK (level <= 30);
