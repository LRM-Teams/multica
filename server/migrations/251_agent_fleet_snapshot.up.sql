-- Workspace-scoped agent fleet rank snapshots (combat rating leaderboard).

CREATE TABLE agent_fleet_snapshot (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    fleet_score NUMERIC(5, 2) NOT NULL DEFAULT 0,
    class_id TEXT NOT NULL DEFAULT 'reserve'
        CHECK (class_id IN ('reserve', 'corvette', 'frigate', 'cruiser', 'battleship', 'dreadnought')),
    fleet_rank INT NOT NULL DEFAULT 0,
    fleet_size INT NOT NULL DEFAULT 0,
    sample_tasks INT NOT NULL DEFAULT 0,
    pillar_delivery NUMERIC(5, 2) NOT NULL DEFAULT 0,
    pillar_evolution NUMERIC(5, 2) NOT NULL DEFAULT 0,
    pillar_growth NUMERIC(5, 2) NOT NULL DEFAULT 0,
    pillar_efficiency NUMERIC(5, 2) NOT NULL DEFAULT 0,
    frozen BOOLEAN NOT NULL DEFAULT false,
    frozen_at TIMESTAMPTZ,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, agent_id)
);

CREATE INDEX idx_agent_fleet_snapshot_workspace_rank
    ON agent_fleet_snapshot (workspace_id, fleet_rank)
    WHERE frozen = false;

CREATE INDEX idx_agent_fleet_snapshot_agent
    ON agent_fleet_snapshot (agent_id);
