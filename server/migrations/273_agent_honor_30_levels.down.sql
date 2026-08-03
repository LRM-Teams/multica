ALTER TABLE agent_honor_state
    DROP CONSTRAINT IF EXISTS agent_honor_state_level_max_check;

UPDATE agent_honor_state
SET level = LEAST(60, GREATEST(1, floor(sqrt(total_xp::numeric / 25))::int + 1)),
    updated_at = now()
WHERE level <> LEAST(60, GREATEST(1, floor(sqrt(total_xp::numeric / 25))::int + 1));
