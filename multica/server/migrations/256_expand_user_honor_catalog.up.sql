-- Expand user honor from 8 visible name tiers / 17 badges to
-- 24 visible name tiers / 51 badges.

INSERT INTO honor_name_style_def (id, css_token, sort_rank, min_level) VALUES
    ('default', 'honor-name--default', 0, 1),
    ('ice', 'honor-name--ice', 5, 3),
    ('member', 'honor-name--member', 10, 5),
    ('emerald', 'honor-name--emerald', 15, 7),
    ('sapphire', 'honor-name--sapphire', 20, 9),
    ('gold', 'honor-name--gold', 25, 12),
    ('coral', 'honor-name--coral', 30, 15),
    ('amethyst', 'honor-name--amethyst', 35, 18),
    ('prismatic', 'honor-name--prismatic', 40, 21),
    ('aurora', 'honor-name--aurora', 45, 24),
    ('glow', 'honor-name--glow', 50, 27),
    ('solar', 'honor-name--solar', 55, 30),
    ('shimmer', 'honor-name--shimmer', 60, 33),
    ('nebula', 'honor-name--nebula', 65, 36),
    ('cyber', 'honor-name--cyber', 70, 39),
    ('animated_prismatic', 'honor-name--animated-prismatic', 75, 42),
    ('plasma', 'honor-name--plasma', 80, 45),
    ('animated_glow', 'honor-name--animated-glow', 85, 48),
    ('eclipse', 'honor-name--eclipse', 90, 51),
    ('nova', 'honor-name--nova', 95, 53),
    ('quantum', 'honor-name--quantum', 100, 55),
    ('celestial', 'honor-name--celestial', 105, 57),
    ('mythic', 'honor-name--mythic', 110, 59),
    ('transcendent', 'honor-name--transcendent', 115, 60),
    -- Founding is a special early identity, not a permanent max-tier override:
    -- level-21 Prismatic and later earned styles supersede it.
    ('founding', 'honor-name--founding', 37, 1)
ON CONFLICT (id) DO UPDATE SET
    css_token = EXCLUDED.css_token,
    sort_rank = EXCLUDED.sort_rank,
    min_level = EXCLUDED.min_level;

INSERT INTO honor_badge_def (
    id, title, description, svg_key, rarity, sort_rank, secret, unlock_rule
) VALUES
    ('lunar_spark', 'Lunar Spark', 'Reached level 2 — the first signal beyond the horizon.', 'moon', 8, 11, false, 'Reach honor level 2'),
    ('comet_trail', 'Comet Trail', 'Reached level 4 — momentum is now visible.', 'comet', 11, 13, false, 'Reach honor level 4'),
    ('asteroid_scout', 'Asteroid Scout', 'Reached level 6 — navigated the first debris field.', 'asteroid', 13, 15, false, 'Reach honor level 6'),
    ('eclipse_watcher', 'Eclipse Watcher', 'Reached level 7 — kept moving through shadow.', 'eclipse', 14, 17, false, 'Reach honor level 7'),
    ('pulsar_ping', 'Pulsar Ping', 'Reached level 9 — a repeatable delivery rhythm.', 'pulsar', 16, 19, false, 'Reach honor level 9'),
    ('solar_sailor', 'Solar Sailor', 'Reached level 11 — progress powered by steady activity.', 'solar_sail', 18, 21, false, 'Reach honor level 11'),
    ('orbital_cadet', 'Orbital Cadet', 'Reached level 13 — operating reliably in team orbit.', 'orbital_station', 20, 23, false, 'Reach honor level 13'),
    ('lunar_architect', 'Lunar Architect', 'Reached level 14 — built a durable foothold.', 'lunar_base', 21, 25, false, 'Reach honor level 14'),
    ('pathfinder', 'Pathfinder', 'Reached level 16 — opened a route others can follow.', 'pathfinder', 24, 29, false, 'Reach honor level 16'),
    ('voyager', 'Voyager', 'Reached level 17 — crossed the familiar boundary.', 'voyager', 25, 31, false, 'Reach honor level 17'),
    ('beacon_keeper', 'Beacon Keeper', 'Reached level 19 — made progress visible to the team.', 'beacon', 28, 35, false, 'Reach honor level 19'),
    ('relay_master', 'Relay Master', 'Reached level 21 — kept collaboration signals moving.', 'relay', 31, 39, false, 'Reach honor level 21'),
    ('archive_seed', 'Archive Seed', 'Reached level 23 — contributions now form a lasting record.', 'archive', 33, 43, false, 'Reach honor level 23'),
    ('constellation_map', 'Constellation Map', 'Reached level 24 — connected isolated wins into a pattern.', 'constellation', 35, 45, false, 'Reach honor level 24'),
    ('aurora_weaver', 'Aurora Weaver', 'Reached level 25 — created a visible field of activity.', 'aurora', 37, 47, false, 'Reach honor level 25'),
    ('galaxy_roamer', 'Galaxy Roamer', 'Reached level 27 — sustained progress across a wider system.', 'galaxy', 39, 51, false, 'Reach honor level 27'),
    ('wormhole_cartographer', 'Wormhole Cartographer', 'Reached level 28 — found shorter paths through hard work.', 'wormhole', 41, 53, false, 'Reach honor level 28'),
    ('terraformer', 'Terraformer', 'Reached level 29 — changed the environment through delivery.', 'terraformer', 43, 55, false, 'Reach honor level 29'),
    ('foundry_heart', 'Foundry Heart', 'Reached level 32 — contribution became a production engine.', 'foundry', 48, 61, false, 'Reach honor level 32'),
    ('nexus_link', 'Nexus Link', 'Reached level 34 — connected people, work, and outcomes.', 'nexus', 52, 63, false, 'Reach honor level 34'),
    ('helix_mind', 'Helix Mind', 'Reached level 37 — repeated contribution became expertise.', 'helix', 58, 69, false, 'Reach honor level 37'),
    ('prism_core', 'Prism Core', 'Reached level 42 — one signal now carries many colors.', 'prism_core', 68, 77, false, 'Reach honor level 42'),
    ('plasma_orb', 'Plasma Orb', 'Reached level 45 — high-energy contribution sustained.', 'plasma_orb', 74, 81, false, 'Reach honor level 45'),
    ('quantum_gate', 'Quantum Gate', 'Reached level 48 — crossed into the late-game honor field.', 'quantum_gate', 82, 87, true, 'Reach honor level 48'),
    ('singularity', 'Singularity', 'Reached level 52 — an exceptional density of contribution.', 'singularity', 88, 92, true, 'Reach honor level 52'),
    ('celestial_crown', 'Celestial Crown', 'Reached level 54 — recognized across the collaboration system.', 'celestial_crown', 91, 94, true, 'Reach honor level 54'),
    ('event_horizon', 'Event Horizon', 'Reached level 56 — entered the legendary progression band.', 'event_horizon', 94, 96, true, 'Reach honor level 56'),
    ('cosmic_tree', 'Cosmic Tree', 'Reached level 58 — long-term contribution now branches outward.', 'cosmic_tree', 97, 97, true, 'Reach honor level 58'),
    ('infinity_engine', 'Infinity Engine', 'Reached level 60 — completed the current honor progression.', 'infinity', 100, 99, true, 'Reach honor level 60'),
    ('signal_architect', 'Signal Architect', 'Reached usage pillar tier 3.', 'photon_ring', 32, 38, false, 'Usage pillar tier 3+'),
    ('chronicle_engine', 'Chronicle Engine', 'Reached usage pillar tier 6.', 'chronosphere', 76, 83, true, 'Usage pillar tier 6+'),
    ('steady_light', 'Steady Light', 'Reached presence pillar tier 4.', 'diamond_star', 46, 57, false, 'Presence pillar tier 4+'),
    ('everpresent', 'Everpresent', 'Reached presence pillar tier 8.', 'supernova', 98, 98, true, 'Presence pillar tier 8+'),
    ('delivery_singularity', 'Delivery Singularity', 'Reached delivery pillar tier 8.', 'black_hole', 99, 98, true, 'Delivery pillar tier 8+')
ON CONFLICT (id) DO UPDATE SET
    title = EXCLUDED.title,
    description = EXCLUDED.description,
    svg_key = EXCLUDED.svg_key,
    rarity = EXCLUDED.rarity,
    sort_rank = EXCLUDED.sort_rank,
    secret = EXCLUDED.secret,
    unlock_rule = EXCLUDED.unlock_rule;
