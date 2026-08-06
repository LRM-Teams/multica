-- Remove references before definitions so rollback cannot leave a manual
-- equipped badge or showcase item pointing at a missing catalog row.
UPDATE user_honor
SET equipped_badge_id = NULL,
    equipped_badge_manual = false,
    updated_at = now()
WHERE equipped_badge_id IN (
    'lunar_spark', 'comet_trail', 'asteroid_scout', 'eclipse_watcher', 'pulsar_ping',
    'solar_sailor', 'orbital_cadet', 'lunar_architect', 'pathfinder', 'voyager',
    'beacon_keeper', 'relay_master', 'archive_seed', 'constellation_map', 'aurora_weaver',
    'galaxy_roamer', 'wormhole_cartographer', 'terraformer', 'foundry_heart', 'nexus_link',
    'helix_mind', 'prism_core', 'plasma_orb', 'quantum_gate', 'singularity',
    'celestial_crown', 'event_horizon', 'cosmic_tree', 'infinity_engine',
    'signal_architect', 'chronicle_engine', 'steady_light', 'everpresent',
    'delivery_singularity'
);

UPDATE user_honor AS honor
SET showcase_badge_ids = COALESCE((
    SELECT array_agg(badge_id ORDER BY ordinal)
    FROM unnest(honor.showcase_badge_ids) WITH ORDINALITY AS badges(badge_id, ordinal)
    WHERE badge_id NOT IN (
        'lunar_spark', 'comet_trail', 'asteroid_scout', 'eclipse_watcher', 'pulsar_ping',
        'solar_sailor', 'orbital_cadet', 'lunar_architect', 'pathfinder', 'voyager',
        'beacon_keeper', 'relay_master', 'archive_seed', 'constellation_map', 'aurora_weaver',
        'galaxy_roamer', 'wormhole_cartographer', 'terraformer', 'foundry_heart', 'nexus_link',
        'helix_mind', 'prism_core', 'plasma_orb', 'quantum_gate', 'singularity',
        'celestial_crown', 'event_horizon', 'cosmic_tree', 'infinity_engine',
        'signal_architect', 'chronicle_engine', 'steady_light', 'everpresent',
        'delivery_singularity'
    )
), '{}');

DELETE FROM user_honor_unlock
WHERE unlock_kind = 'badge'
  AND def_id IN (
    'lunar_spark', 'comet_trail', 'asteroid_scout', 'eclipse_watcher', 'pulsar_ping',
    'solar_sailor', 'orbital_cadet', 'lunar_architect', 'pathfinder', 'voyager',
    'beacon_keeper', 'relay_master', 'archive_seed', 'constellation_map', 'aurora_weaver',
    'galaxy_roamer', 'wormhole_cartographer', 'terraformer', 'foundry_heart', 'nexus_link',
    'helix_mind', 'prism_core', 'plasma_orb', 'quantum_gate', 'singularity',
    'celestial_crown', 'event_horizon', 'cosmic_tree', 'infinity_engine',
    'signal_architect', 'chronicle_engine', 'steady_light', 'everpresent',
    'delivery_singularity'
);

DELETE FROM honor_badge_def
WHERE id IN (
    'lunar_spark', 'comet_trail', 'asteroid_scout', 'eclipse_watcher', 'pulsar_ping',
    'solar_sailor', 'orbital_cadet', 'lunar_architect', 'pathfinder', 'voyager',
    'beacon_keeper', 'relay_master', 'archive_seed', 'constellation_map', 'aurora_weaver',
    'galaxy_roamer', 'wormhole_cartographer', 'terraformer', 'foundry_heart', 'nexus_link',
    'helix_mind', 'prism_core', 'plasma_orb', 'quantum_gate', 'singularity',
    'celestial_crown', 'event_horizon', 'cosmic_tree', 'infinity_engine',
    'signal_architect', 'chronicle_engine', 'steady_light', 'everpresent',
    'delivery_singularity'
);

DELETE FROM user_honor_unlock
WHERE unlock_kind = 'style'
  AND def_id IN (
    'ice', 'emerald', 'sapphire', 'coral', 'amethyst', 'aurora', 'solar',
    'nebula', 'cyber', 'plasma', 'eclipse', 'nova', 'quantum', 'celestial',
    'mythic', 'transcendent'
);

DELETE FROM honor_name_style_def
WHERE id IN (
    'ice', 'emerald', 'sapphire', 'coral', 'amethyst', 'aurora', 'solar',
    'nebula', 'cyber', 'plasma', 'eclipse', 'nova', 'quantum', 'celestial',
    'mythic', 'transcendent'
);

UPDATE honor_name_style_def SET sort_rank = 0, min_level = 1 WHERE id = 'default';
UPDATE honor_name_style_def SET sort_rank = 10, min_level = 5 WHERE id = 'member';
UPDATE honor_name_style_def SET sort_rank = 20, min_level = 12 WHERE id = 'gold';
UPDATE honor_name_style_def SET sort_rank = 25, min_level = 1 WHERE id = 'founding';
UPDATE honor_name_style_def SET sort_rank = 30, min_level = 20 WHERE id = 'prismatic';
UPDATE honor_name_style_def SET sort_rank = 40, min_level = 28 WHERE id = 'glow';
UPDATE honor_name_style_def SET sort_rank = 50, min_level = 35 WHERE id = 'shimmer';
UPDATE honor_name_style_def SET sort_rank = 60, min_level = 42 WHERE id = 'animated_prismatic';
UPDATE honor_name_style_def SET sort_rank = 70, min_level = 48 WHERE id = 'animated_glow';
