-- Durable agent honor: lifetime XP, achievements, showcase, rules, audit, and fleet history.

CREATE TABLE agent_honor_state (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    total_xp BIGINT NOT NULL DEFAULT 0 CHECK (total_xp >= 0),
    level INT NOT NULL DEFAULT 1 CHECK (level >= 1),
    equipped_achievement_id TEXT,
    showcase_achievement_ids TEXT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, agent_id),
    CHECK (cardinality(showcase_achievement_ids) <= 3)
);

CREATE INDEX agent_honor_state_agent
    ON agent_honor_state (agent_id);

CREATE TABLE agent_honor_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('delivery', 'achievement', 'manual')),
    source_ref TEXT NOT NULL,
    xp_delta INT NOT NULL CHECK (xp_delta <> 0),
    reason TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}',
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, agent_id, event_type, source_ref)
);

CREATE INDEX agent_honor_event_agent_created
    ON agent_honor_event (workspace_id, agent_id, created_at DESC);

CREATE INDEX agent_honor_event_agent
    ON agent_honor_event (agent_id);

CREATE TABLE agent_honor_unlock (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    achievement_id TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('auto', 'manual', 'backfill')),
    unlocked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, agent_id, achievement_id)
);

CREATE INDEX agent_honor_unlock_achievement
    ON agent_honor_unlock (achievement_id, unlocked_at);

CREATE INDEX agent_honor_unlock_agent
    ON agent_honor_unlock (agent_id);

CREATE TABLE agent_honor_rule_config (
    workspace_id UUID PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
    version INT NOT NULL DEFAULT 1 CHECK (version >= 1),
    config JSONB NOT NULL DEFAULT '{}',
    updated_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agent_honor_admin_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    action TEXT NOT NULL CHECK (action IN ('rules.update', 'xp.grant', 'achievement.grant', 'achievement.revoke')),
    details JSONB NOT NULL DEFAULT '{}',
    created_by UUID NOT NULL REFERENCES "user"(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agent_honor_admin_audit_workspace_created
    ON agent_honor_admin_audit (workspace_id, created_at DESC);

CREATE INDEX agent_honor_admin_audit_agent
    ON agent_honor_admin_audit (agent_id);

CREATE TABLE agent_fleet_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    fleet_score NUMERIC(5, 2) NOT NULL,
    class_id TEXT NOT NULL
        CHECK (class_id IN ('reserve', 'corvette', 'frigate', 'cruiser', 'battleship', 'dreadnought')),
    fleet_rank INT NOT NULL,
    fleet_size INT NOT NULL,
    sample_tasks INT NOT NULL,
    pillar_delivery NUMERIC(5, 2) NOT NULL,
    pillar_evolution NUMERIC(5, 2) NOT NULL,
    pillar_growth NUMERIC(5, 2) NOT NULL,
    pillar_efficiency NUMERIC(5, 2) NOT NULL,
    trigger_reason TEXT NOT NULL DEFAULT 'refresh',
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agent_fleet_history_agent_recorded
    ON agent_fleet_history (workspace_id, agent_id, recorded_at DESC);

CREATE INDEX agent_fleet_history_agent
    ON agent_fleet_history (agent_id);

CREATE OR REPLACE FUNCTION create_agent_honor_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO agent_honor_state (workspace_id, agent_id)
    VALUES (NEW.workspace_id, NEW.id)
    ON CONFLICT DO NOTHING;
    INSERT INTO agent_fleet_snapshot (workspace_id, agent_id)
    VALUES (NEW.workspace_id, NEW.id)
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_create_honor_state
AFTER INSERT ON agent
FOR EACH ROW EXECUTE FUNCTION create_agent_honor_state();

INSERT INTO agent_honor_state (workspace_id, agent_id)
SELECT workspace_id, id FROM agent
ON CONFLICT DO NOTHING;

-- Event-driven fleet refresh needs a durable Reserve row even before an agent
-- completes its first task. Existing computed snapshots are preserved.
INSERT INTO agent_fleet_snapshot (workspace_id, agent_id)
SELECT workspace_id, id
FROM agent
WHERE archived_at IS NULL
ON CONFLICT DO NOTHING;

-- Existing accepted deliveries become permanent, idempotent lifetime XP.
INSERT INTO agent_honor_event (
    workspace_id, agent_id, event_type, source_ref, xp_delta, reason, created_at
)
SELECT
    e.workspace_id,
    e.agent_id,
    'delivery',
    e.id::text,
    10,
    'Accepted delivery',
    COALESCE(e.completed_at, e.terminal_at, e.updated_at)
FROM agent_inbox_event e
WHERE e.status = 'acked'
  AND e.terminal_outcome = 'completed'
  AND COALESCE(e.context->>'type', '') <> 'agent_radar'
ON CONFLICT DO NOTHING;

-- Backfill deterministic milestone achievements that can be derived without replaying events.
WITH metrics AS (
    SELECT
        a.workspace_id,
        a.id AS agent_id,
        (
            SELECT COUNT(*)
            FROM agent_inbox_event e
            WHERE e.agent_id = a.id
              AND e.status = 'acked'
              AND e.terminal_outcome = 'completed'
              AND COALESCE(e.context->>'type', '') <> 'agent_radar'
        )::bigint AS completed_count,
        (
            SELECT COUNT(*)
            FROM agent_memory_write_event m
            WHERE m.agent_id = a.id
        )::bigint AS memory_writes,
        (
            SELECT COUNT(*)
            FROM evolution_unit_submission s
            WHERE s.source_agent_id = a.id AND s.status = 'promoted'
        )::bigint AS evolution_promotions,
        (
            SELECT COUNT(DISTINCT i.project_id)
            FROM agent_inbox_event e
            JOIN issue i ON i.id = e.issue_id
            WHERE e.agent_id = a.id
              AND e.status = 'acked'
              AND e.terminal_outcome = 'completed'
              AND i.project_id IS NOT NULL
        )::bigint AS distinct_projects
    FROM agent a
),
earned AS (
    SELECT workspace_id, agent_id, achievement_id
    FROM metrics
    CROSS JOIN LATERAL (
        VALUES
            ('first_launch', completed_count >= 1),
            ('proven_crew', completed_count >= 10),
            ('veteran_core', completed_count >= 50),
            ('centurion', completed_count >= 100),
            ('memory_spark', memory_writes >= 3),
            ('memory_archive', memory_writes >= 24),
            ('memory_constellation', memory_writes >= 100),
            ('evolution_seed', evolution_promotions >= 1),
            ('evolution_engine', evolution_promotions >= 10),
            ('deep_space_explorer', distinct_projects >= 3)
    ) AS achievement(achievement_id, unlocked)
    WHERE unlocked
)
INSERT INTO agent_honor_unlock (workspace_id, agent_id, achievement_id, source)
SELECT workspace_id, agent_id, achievement_id, 'backfill'
FROM earned
ON CONFLICT DO NOTHING;

INSERT INTO agent_honor_event (
    workspace_id, agent_id, event_type, source_ref, xp_delta, reason
)
SELECT
    u.workspace_id,
    u.agent_id,
    'achievement',
    u.achievement_id,
    CASE u.achievement_id
        WHEN 'first_launch' THEN 25
        WHEN 'proven_crew' THEN 50
        WHEN 'veteran_core' THEN 125
        WHEN 'centurion' THEN 250
        WHEN 'memory_spark' THEN 30
        WHEN 'memory_archive' THEN 100
        WHEN 'memory_constellation' THEN 300
        WHEN 'evolution_seed' THEN 100
        WHEN 'evolution_engine' THEN 350
        WHEN 'deep_space_explorer' THEN 125
        ELSE 25
    END,
    'Achievement unlocked'
FROM agent_honor_unlock u
ON CONFLICT DO NOTHING;

UPDATE agent_honor_state state
SET total_xp = totals.total_xp,
    level = LEAST(60, GREATEST(1, floor(sqrt(totals.total_xp::numeric / 25))::int + 1)),
    updated_at = now()
FROM (
    SELECT workspace_id, agent_id, GREATEST(0, SUM(xp_delta))::bigint AS total_xp
    FROM agent_honor_event
    GROUP BY workspace_id, agent_id
) totals
WHERE state.workspace_id = totals.workspace_id
  AND state.agent_id = totals.agent_id;
