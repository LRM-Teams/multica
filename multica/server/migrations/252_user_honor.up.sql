-- Platform-global user honor: levels, badges, name styles, XP ledger.

CREATE TABLE user_honor (
    user_id UUID PRIMARY KEY REFERENCES "user"(id) ON DELETE CASCADE,
    total_xp BIGINT NOT NULL DEFAULT 0 CHECK (total_xp >= 0),
    level INT NOT NULL DEFAULT 1 CHECK (level >= 1),
    equipped_badge_id TEXT,
    membership_tier TEXT,
    membership_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE honor_badge_def (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    svg_key TEXT NOT NULL,
    rarity INT NOT NULL DEFAULT 0,
    sort_rank INT NOT NULL DEFAULT 0
);

CREATE TABLE honor_name_style_def (
    id TEXT PRIMARY KEY,
    css_token TEXT NOT NULL,
    sort_rank INT NOT NULL DEFAULT 0,
    min_level INT NOT NULL DEFAULT 1
);

CREATE TABLE user_honor_unlock (
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    unlock_kind TEXT NOT NULL CHECK (unlock_kind IN ('badge', 'style')),
    def_id TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('auto', 'ops', 'founding')),
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, unlock_kind, def_id)
);

CREATE INDEX user_honor_unlock_user_id ON user_honor_unlock(user_id);

CREATE TABLE user_xp_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    pillar TEXT NOT NULL CHECK (pillar IN ('usage', 'presence', 'delivery', 'community')),
    action_type TEXT NOT NULL,
    xp_delta INT NOT NULL CHECK (xp_delta > 0),
    ref_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX user_xp_ledger_user_created ON user_xp_ledger(user_id, created_at DESC);
CREATE INDEX user_xp_ledger_user_action_day ON user_xp_ledger(user_id, action_type, created_at);

CREATE TABLE user_pillar_progress (
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    pillar TEXT NOT NULL CHECK (pillar IN ('usage', 'presence', 'delivery', 'community')),
    counter_value BIGINT NOT NULL DEFAULT 0 CHECK (counter_value >= 0),
    tier INT NOT NULL DEFAULT 0 CHECK (tier >= 0 AND tier <= 8),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, pillar)
);

CREATE TABLE user_honor_grant (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    grant_kind TEXT NOT NULL CHECK (grant_kind IN ('badge', 'style')),
    def_id TEXT NOT NULL,
    granted_by UUID REFERENCES "user"(id),
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX user_honor_grant_user_id ON user_honor_grant(user_id);

-- Seed badge catalog (svg_key maps to packages/ui honor icons).
INSERT INTO honor_badge_def (id, title, description, svg_key, rarity, sort_rank) VALUES
    ('founding', 'Genesis Nebula', 'Registered before August 1, 2026 — first light of the collaboration universe.', 'genesis_nebula', 100, 100),
    ('stardust', 'Stardust', 'Reached level 3 — your journey begins among the particles.', 'stardust', 10, 10),
    ('mercury', 'Mercury', 'Reached level 5 — swift orbit around the inner core.', 'mercury', 12, 12),
    ('venus', 'Venus', 'Reached level 8 — radiant presence in the inner system.', 'venus', 14, 14),
    ('earth', 'Earth', 'Reached level 10 — anchor of collaboration.', 'earth', 16, 16),
    ('mars', 'Mars', 'Reached level 12 — frontier builder spirit.', 'mars', 18, 18),
    ('jupiter', 'Jupiter', 'Reached level 15 — gravitational pull of delivery.', 'jupiter', 22, 22),
    ('saturn', 'Saturn', 'Reached level 18 — rings of sustained contribution.', 'saturn', 26, 26),
    ('uranus', 'Uranus', 'Reached level 22 — tilted brilliance, steady glow tier III.', 'uranus', 30, 30),
    ('neptune', 'Neptune', 'Reached level 26 — deep-space navigator.', 'neptune', 34, 34),
    ('pluto', 'Pluto', 'Reached level 30 — distant but enduring.', 'pluto', 38, 38),
    ('veteran', 'Red Giant', 'Reached level 20 — stellar veteran of the fleet.', 'red_giant', 60, 60),
    ('red_dwarf', 'Red Dwarf', 'Reached level 35 — long-burning stellar class.', 'red_dwarf', 65, 65),
    ('blue_giant', 'Blue Giant', 'Reached level 40 — high-luminosity contributor.', 'blue_giant', 72, 72),
    ('quasar', 'Quasar', 'Reached level 50 — legendary deep-space beacon.', 'quasar', 90, 90),
    ('builder', 'Forge Ring', 'Delivery pillar tier 4 or higher.', 'forge_ring', 40, 40),
    ('collaborator', 'Twin Stars', 'Community pillar tier 3 or higher.', 'twin_stars', 30, 30);

INSERT INTO honor_name_style_def (id, css_token, sort_rank, min_level) VALUES
    ('default', 'honor-name--default', 0, 1),
    ('member', 'honor-name--member', 10, 5),
    ('gold', 'honor-name--gold', 20, 12),
    ('founding', 'honor-name--founding', 25, 1),
    ('prismatic', 'honor-name--prismatic', 30, 20),
    ('glow', 'honor-name--glow', 40, 28),
    ('shimmer', 'honor-name--shimmer', 50, 35),
    ('animated_prismatic', 'honor-name--animated-prismatic', 60, 42),
    ('animated_glow', 'honor-name--animated-glow', 70, 48);
