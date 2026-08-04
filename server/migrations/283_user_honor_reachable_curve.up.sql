-- Recalculate stored honor levels after replacing the exponential level 21-80
-- tail with non-demoting piecewise increments.
WITH RECURSIVE honor_thresholds(level, total_xp) AS (
    VALUES (1, 0::bigint)
    UNION ALL
    SELECT
        level + 1,
        total_xp + CASE
            WHEN level + 1 <= 20
                THEN FLOOR(10 * POWER(1.15, level - 1))::bigint
            WHEN level + 1 <= 40
                THEN (140 + 20 * ((level + 1) - 21))::bigint
            WHEN level + 1 <= 60
                THEN (550 + 70 * ((level + 1) - 41))::bigint
            WHEN level + 1 <= 70
                THEN (2500 + 250 * ((level + 1) - 61))::bigint
            ELSE (5000 + 500 * ((level + 1) - 71))::bigint
        END
    FROM honor_thresholds
    WHERE level < 80
), recalculated AS (
    SELECT user_honor.user_id, MAX(honor_thresholds.level)::int AS level
    FROM user_honor
    JOIN honor_thresholds ON honor_thresholds.total_xp <= user_honor.total_xp
    GROUP BY user_honor.user_id
)
UPDATE user_honor
SET level = recalculated.level,
    updated_at = now()
FROM recalculated
WHERE user_honor.user_id = recalculated.user_id
  AND user_honor.level IS DISTINCT FROM recalculated.level;

-- Users promoted by the new curve receive every level-gated name style now,
-- rather than waiting for their next XP-producing action.
INSERT INTO user_honor_unlock (user_id, unlock_kind, def_id, source)
SELECT user_honor.user_id, 'style', honor_name_style_def.id, 'auto'
FROM user_honor
JOIN honor_name_style_def ON honor_name_style_def.min_level <= user_honor.level
ON CONFLICT (user_id, unlock_kind, def_id) DO NOTHING;

-- Badge level requirements live in the application catalog. This one-time
-- migration mirrors only the level-gated subset so existing users converge at
-- deployment without changing pillar or founding awards.
WITH level_badges(id, min_level) AS (
    VALUES
        ('lunar_spark', 2),
        ('stardust', 3),
        ('comet_trail', 4),
        ('mercury', 5),
        ('asteroid_scout', 6),
        ('eclipse_watcher', 7),
        ('venus', 8),
        ('pulsar_ping', 9),
        ('earth', 10),
        ('solar_sailor', 11),
        ('mars', 12),
        ('orbital_cadet', 13),
        ('lunar_architect', 14),
        ('jupiter', 15),
        ('pathfinder', 16),
        ('voyager', 17),
        ('saturn', 18),
        ('beacon_keeper', 19),
        ('veteran', 20),
        ('relay_master', 21),
        ('uranus', 22),
        ('archive_seed', 23),
        ('constellation_map', 24),
        ('aurora_weaver', 25),
        ('neptune', 26),
        ('galaxy_roamer', 27),
        ('wormhole_cartographer', 28),
        ('terraformer', 29),
        ('pluto', 30),
        ('foundry_heart', 32),
        ('nexus_link', 34),
        ('red_dwarf', 35),
        ('helix_mind', 37),
        ('blue_giant', 40),
        ('prism_core', 42),
        ('plasma_orb', 45),
        ('quantum_gate', 48),
        ('quasar', 50),
        ('singularity', 52),
        ('celestial_crown', 54),
        ('event_horizon', 56),
        ('cosmic_tree', 58),
        ('infinity_engine', 80)
)
INSERT INTO user_honor_unlock (user_id, unlock_kind, def_id, source)
SELECT user_honor.user_id, 'badge', level_badges.id, 'auto'
FROM user_honor
JOIN level_badges ON level_badges.min_level <= user_honor.level
JOIN honor_badge_def ON honor_badge_def.id = level_badges.id
ON CONFLICT (user_id, unlock_kind, def_id) DO NOTHING;
